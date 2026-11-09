package main

import (
	"cmp"
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// gateSource is what every task's gates read alike.
type gateSource struct {
	events []ledger.Event
	main   string           // where the branch config lives
	eff    *facts.Effective // the flows, the paces and the review every task resolves against
	review facts.Review
	store  *lore.Store
	host   *host
	shared string // the storage root every checkout shares
	// driveFlows is the flow each drive record holds its tasks under, by
	// lead id: a task the drive has not opened has no branch to record it
	driveFlows map[string]driveFlow
}

// A driveFlow is the flow and the --pace a drive record names, and whether
// that drive's driver still runs: only a running one can be opening a
// branch right now.
type driveFlow struct {
	name    string
	pace    string
	running bool
}

// storageFor is where a task's findings and verdicts are: its checkout's
// storage root, else the shared one.
func (src gateSource) storageFor(ctx context.Context, root string) string {
	if root != "" {
		return layout.Locate(ctx, root).StorageRoot.Path()
	}

	return src.shared
}

// driveFlows maps each task of a drive neither cancelled nor done to the
// flow the drive took, the newest drive winning. A drive without --flow
// records none, read as default: an area's pick is ship's to make.
func driveFlows(root string) map[string]driveFlow {
	out := map[string]driveFlow{}

	for _, d := range drives.Active(root) {
		for _, t := range d.Tasks {
			if lead := ref.TypedLead(t.ID); out[lead].name == "" {
				// a record that no longer parses keeps its flow, under no pace
				a, _ := parseDriveArgs(d.Args)
				out[lead] = driveFlow{name: cmp.Or(d.Flow, "default"), pace: a.pace, running: d.Running}
			}
		}
	}

	return out
}

func (src gateSource) gatesOf(ctx context.Context, id, branch, root, storage string, run *state.Run) []gate {
	in := gateInput{
		task:       id,
		branch:     branch,
		checkout:   root,
		run:        run,
		events:     src.events,
		remoteFlow: src.review.Remote(),
		outcomes:   seriesOutcomes(src.store, storage, id, branch != ""),
		now:        time.Now(),
	}

	// the branch's record, else the drive that will open the task: a
	// stopped drive's record speaks only for a task with no branch yet,
	// since a branch it never recorded may have been started another way
	name, pace := "default", ""
	if d := src.driveFlows[strings.ToUpper(id)]; d.running || branch == "" {
		name = cmp.Or(d.name, name)
	}

	// a branch records the pace it was opened at; before one exists, the
	// drive that will open it holds the pace
	if branch == "" {
		pace = src.driveFlows[strings.ToUpper(id)].pace
	}

	if branch != "" && src.main != "" {
		in.driven = branchmeta.Out(ctx, src.main, branch, branchmeta.Drive)
		// the record says drive finishes the branch, and outlives a halted or
		// killed drive; what finish is doing counts only under the lease
		rec, ok := branchmeta.ParseDriveFinish(branchmeta.Out(ctx, src.main, branch, branchmeta.Finish))
		if _, held := drives.Probe(src.shared, id); ok && held {
			in.finish = rec
		}

		in.finishes = ok

		if recorded := branchmeta.Out(ctx, src.main, branch, branchmeta.Flow); recorded != "" {
			name = recorded
		}
	}

	if _, ok := src.eff.Flows[name]; !ok {
		name = "default"
	}

	in.pipeline = src.pipeline(ctx, name, branch, pace)

	if src.host != nil && branch != "" && src.review.Remote() {
		h := *src.host
		if root != "" {
			h = h.at(root)
		}

		in.remote = func(ctx context.Context) remoteState {
			return readRemote(ctx, h, src.review, branch)
		}
	}

	return taskGates(ctx, in)
}

// pipeline is what ship runs of the named flow under the task's pace: pace
// when given, else the one its branch records, else the default. A pace that
// no longer resolves leaves the flow as declared, since status must still
// show the task.
func (src gateSource) pipeline(ctx context.Context, name, branch, pace string) flow.Flow {
	declared := src.eff.Flows[name]
	if src.eff.PaceSet == nil {
		return declared.Ship()
	}

	in := runInput{pace: pace}
	if src.main != "" {
		in.main, in.branch = src.main, branch
	}

	run, err := resolveRun(ctx, src.eff, in)
	if err != nil {
		return declared.Ship()
	}

	entries, err := run.entries(name)
	if err != nil {
		return declared.Ship()
	}

	return entries.Ship()
}

func readRemote(ctx context.Context, h host, f facts.Review, branch string) remoteState {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()

	p, err := h.PR(ctx, branch)

	switch {
	case ctx.Err() != nil:
		return remoteState{err: ctx.Err()}
	case errors.Is(err, forge.ErrNotFound):
		return remoteState{}
	case err != nil:
		return remoteState{err: err}
	case p.URL == "":
		return remoteState{}
	}

	rv, err := newReviewer(ctx, h, f, path.Base(p.URL))
	if err == nil {
		var r remoteReview

		r, err = rv.latest(ctx)
		if err == nil {
			return remoteState{found: true, done: r.Done, handled: r.Handled, settled: r.Settled}
		}
	}

	switch {
	case ctx.Err() != nil:
		return remoteState{err: ctx.Err()}
	case errors.Is(err, errNoReview):
		return remoteState{}
	}

	return remoteState{err: err}
}
