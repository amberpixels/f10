package statusview

import (
	"cmp"
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/state"
)

// gateSource is what every task's gates read alike.
type gateSource struct {
	events []ledger.Event
	Main   string           // where the branch config lives
	Eff    *facts.Effective // the flows, the paces and the review every task resolves against
	review facts.Review
	store  *lore.Store
	host   *hostops.Host
	Shared string // the storage root every checkout shares
	// driveFlows is the flow each drive record holds its tasks under, by
	// lead id: a task the drive has not opened has no branch to record it
	driveFlows map[string]driveFlow
}

// StorageFor is where a task's findings and verdicts are: its checkout's
// storage root, else the shared one.
func (src gateSource) StorageFor(ctx context.Context, root string) string {
	if root != "" {
		return layout.Locate(ctx, root).StorageRoot.Path()
	}

	return src.Shared
}

// Pipeline is what ship runs of the named flow under the task's pace: pace
// when given, else the one its branch records, else the default. A pace that
// no longer resolves leaves the flow as declared, since status must still
// show the task.
func (src gateSource) Pipeline(ctx context.Context, name, branch, pace string) flow.Flow {
	declared := src.Eff.Flows[name]
	if src.Eff.PaceSet == nil {
		return declared.Ship()
	}

	in := runplan.RunInput{Pace: pace}
	if src.Main != "" {
		in.Main, in.Branch = src.Main, branch
	}

	run, err := runplan.ResolveRun(ctx, src.Eff, in)
	if err != nil {
		return declared.Ship()
	}

	entries, err := run.Entries(name)
	if err != nil {
		return declared.Ship()
	}

	return entries.Ship()
}

func (src gateSource) gatesOf(ctx context.Context, id, branch, root, storage string, run *state.Run) []gate {
	in := gateInput{
		Task:       id,
		Branch:     branch,
		Checkout:   root,
		Run:        run,
		Events:     src.events,
		RemoteFlow: src.review.Remote(),
		Outcomes:   SeriesOutcomes(src.store, storage, id, branch != ""),
		Now:        time.Now(),
	}

	// the branch's record, else the drive that will open the task: a
	// stopped drive's record speaks only for a task with no branch yet,
	// since a branch it never recorded may have been started another way
	name, pace := "default", ""
	if d := src.driveFlows[strings.ToUpper(id)]; d.Running || branch == "" {
		name = cmp.Or(d.Name, name)
	}

	// a branch records the pace it was opened at; before one exists, the
	// drive that will open it holds the pace
	if branch == "" {
		pace = src.driveFlows[strings.ToUpper(id)].pace
	}

	if branch != "" && src.Main != "" {
		in.Driven = branchmeta.Out(ctx, src.Main, branch, branchmeta.Drive)
		// the record says drive finishes the branch, and outlives a halted or
		// killed drive; what finish is doing counts only under the lease
		rec, ok := branchmeta.ParseDriveFinish(branchmeta.Out(ctx, src.Main, branch, branchmeta.Finish))
		if _, held := drivestore.Probe(src.Shared, id); ok && held {
			in.Finish = rec
		}

		in.Finishes = ok

		if recorded := branchmeta.Out(ctx, src.Main, branch, branchmeta.Flow); recorded != "" {
			name = recorded
		}
	}

	if _, ok := src.Eff.Flows[name]; !ok {
		name = "default"
	}

	in.Pipeline = src.Pipeline(ctx, name, branch, pace)

	if src.host != nil && branch != "" && src.review.Remote() {
		h := *src.host
		if root != "" {
			h = h.In(root)
		}

		in.Remote = func(ctx context.Context) remoteState {
			return readRemote(ctx, h, src.review, branch)
		}
	}

	return taskGates(ctx, in)
}

// A driveFlow is the flow and the --pace a drive record names, and whether
// that drive's driver still runs: only a running one can be opening a
// branch right now.
type driveFlow struct {
	Name    string
	pace    string
	Running bool
}

// driveFlows maps each task of a drive neither cancelled nor done to the
// flow the drive took, the newest drive winning. A drive without --flow
// records none, read as default: an area's pick is ship's to make.
func driveFlows(active []drivestore.Live) map[string]driveFlow {
	out := map[string]driveFlow{}

	for _, d := range active {
		for _, t := range d.Tasks {
			if lead := ref.TypedLead(t.ID); out[lead].Name == "" {
				// a record that no longer parses keeps its flow, under no pace
				a, _ := drivectl.ParseDriveArgs(d.Args)
				out[lead] = driveFlow{Name: cmp.Or(d.Flow, "default"), pace: a.Pace, Running: d.Running}
			}
		}
	}

	return out
}

func readRemote(ctx context.Context, h hostops.Host, f facts.Review, branch string) remoteState {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()

	p, err := h.PR(ctx, branch)

	switch {
	case ctx.Err() != nil:
		return remoteState{Err: ctx.Err()}
	case errors.Is(err, forge.ErrNotFound):
		return remoteState{}
	case err != nil:
		return remoteState{Err: err}
	case p.URL == "":
		return remoteState{}
	}

	rv, err := reviews.NewReviewer(ctx, h, f, path.Base(p.URL))
	if err == nil {
		var r reviews.RemoteReview

		r, err = rv.Latest(ctx)
		if err == nil {
			return remoteState{Found: true, Done: r.Done, Handled: r.Handled, Settled: r.Settled}
		}
	}

	switch {
	case ctx.Err() != nil:
		return remoteState{Err: ctx.Err()}
	case errors.Is(err, reviews.ErrNoReview):
		return remoteState{}
	}

	return remoteState{Err: err}
}
