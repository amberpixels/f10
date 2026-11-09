package main

import (
	"cmp"
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// gateSource is what every task's gates read alike.
type gateSource struct {
	events []ledger.Event
	main   string // where the branch config lives
	flows  map[string]flow.Flow
	review facts.Review
	store  *lore.Store
	host   *host
	shared string // the storage root every checkout shares
	// driveFlows is the flow each drive record holds its tasks under, by
	// lead id: a task the drive has not opened has no branch to record it
	driveFlows map[string]driveFlow
}

// A driveFlow is the flow a drive record names, and whether that drive's
// driver still runs: only a running one can be opening a branch right now.
type driveFlow struct {
	name    string
	running bool
}

// storageFor is where a task's findings and verdicts are: its checkout's
// storage root, else where its last run left them.
func (src gateSource) storageFor(ctx context.Context, root string, run *state.Run) string {
	if root != "" {
		return layout.Locate(ctx, root).StorageRoot
	}

	if run == nil {
		return src.shared
	}

	return finishedStorage(src.shared, run.Root)
}

// driveFlows maps each task of a drive neither cancelled nor done to the
// flow the drive took, the newest drive winning. A drive without --flow
// records none, read as default: an area's pick is ship's to make.
func driveFlows(root string) map[string]driveFlow {
	recs, err := drives.List(root)
	if err != nil {
		return nil
	}

	out := map[string]driveFlow{}

	for _, r := range recs {
		if r.Cancelled != nil || r.Done() {
			continue
		}

		_, running := drives.Probe(root, drives.DriveKey(r.Number))

		for _, t := range r.Tasks {
			lead, _, _ := strings.Cut(t.ID, ref.Separator)
			lead, _ = ref.SplitSuffix(lead)

			if key := strings.ToUpper(lead); out[key].name == "" {
				out[key] = driveFlow{name: cmp.Or(r.Flow, "default"), running: running}
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
	name := "default"
	if d := src.driveFlows[strings.ToUpper(id)]; d.running || branch == "" {
		name = cmp.Or(d.name, name)
	}

	if branch != "" && src.main != "" {
		in.driven = gitx.Out(ctx, src.main, "config", "--get", "branch."+branch+"."+cfgDrive)
		// the record says drive finishes the branch, and outlives a halted or
		// killed drive; what finish is doing counts only under the lease
		rec, ok := ref.ParseDriveFinish(gitx.Out(ctx, src.main, "config", "--get", "branch."+branch+"."+ref.FinishKey))
		if _, held := drives.Probe(src.shared, id); ok && held {
			in.finish = rec
		}

		in.finishes = ok

		if recorded := gitx.Out(ctx, src.main, "config", "--get", "branch."+branch+"."+cfgFlow); recorded != "" {
			name = recorded
		}
	}

	if _, ok := src.flows[name]; !ok {
		name = "default"
	}

	in.pipeline = src.flows[name].Ship()

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

func readRemote(ctx context.Context, h host, f facts.Review, branch string) remoteState {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()

	p, err := h.pull(ctx, branch)

	switch {
	case ctx.Err() != nil:
		return remoteState{err: ctx.Err()}
	case noSuchPull(err):
		return remoteState{}
	case err != nil:
		return remoteState{err: err}
	case p.url() == "":
		return remoteState{}
	}

	rv, err := newReviewer(ctx, h, f, path.Base(p.url()))
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
