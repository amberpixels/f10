package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"runtime"
	"slices"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// defaultParallel is a drive's cap with no --parallel: half the cores, since
// each agent's verify uses several, held between 2 and 8.
func defaultParallel() int { return min(max(runtime.NumCPU()/2, 2), 8) }

// holdsSlot: a run on its chain keeps its slot from one skill to the next;
// one parked, waiting or queued has given it up.
func (r *driveRun) holdsSlot() bool { return r.active() && !r.parked && !r.waiting && !r.queued }

// slotFree reports whether r may send a skill under the cap. A turn already
// running in its workspace takes a slot past the cap: the drive cannot stop it.
func (in *driveInput) slotFree(r *driveRun) bool {
	if in.parallel <= 0 || in.busy(r) < in.parallel {
		return true
	}

	ws := workspaceByID(in.wss, r.o.ws.ID)

	return ws != nil && ws.Status == agentctl.StatusWorking
}

// busy counts the runs holding a slot, except.
func (in *driveInput) busy(except *driveRun) int {
	n := 0

	for _, r := range in.runs {
		if r != except && r.holdsSlot() {
			n++
		}
	}

	return n
}

func (in *driveInput) queue(w io.Writer, r *driveRun) {
	if r.queued {
		return
	}

	r.queued = true
	fmt.Fprintf(w, "%s: queued, %d running\n", r.task.ref.ID, in.busy(r))
	in.saveTask(r, func(t *drives.Task) { t.Queued = true })
}

func (in *driveInput) unqueue(r *driveRun) {
	if !r.queued {
		return
	}

	r.queued = false
	in.saveTask(r, func(t *drives.Task) { t.Queued = false })
}

// An openGate decides, for one startRunnable pass, which released tasks
// open under the cap. The zero gate admits every task.
type openGate struct {
	on    bool
	limit int
	wss   []agentctl.Workspace
	trees map[string]string // a task's branch to its checkout, for a task not opened yet
	open  int               // active and asking tasks with a workspace, this pass's opens included
	full  bool              // a task queued: every later one queues behind it
}

// openGate reads the workspaces and checkouts only when the cap can bind:
// a drive no longer than its cap never reaches it.
func (in *driveInput) openGate(ctx context.Context, runs []*driveRun, wss []agentctl.Workspace) (*openGate, error) {
	if in.parallel <= 0 || len(runs) <= in.parallel {
		return &openGate{}, nil
	}

	if wss == nil {
		var err error
		if wss, err = agents(in.main).Workspaces(ctx); err != nil {
			return nil, err
		}

		in.wss = wss
	}

	trees, err := gitx.Worktrees(ctx, in.main)
	if err != nil {
		return nil, err
	}

	g := &openGate{on: true, limit: in.parallel, wss: wss, trees: map[string]string{}}
	for _, t := range trees {
		if t.Branch != "" {
			g.trees[t.Branch] = t.Path
		}
	}

	// an earlier run of this drive may have left agents open, and they
	// count; a done or halted task's agent is the user's now, and counting
	// it would stop a drive without finish with tasks left
	for _, r := range runs {
		if (r.end == nil || r.end.code == exitAsk) && g.workspace(r) != nil {
			g.open++
		}
	}

	return g, nil
}

// workspace is the one showing r's checkout, nil for none.
func (g *openGate) workspace(r *driveRun) *agentctl.Workspace {
	path := r.o.path
	if !r.opened && r.task.branch != "" {
		path = g.trees[r.task.branch]
	}

	if path == "" {
		return nil
	}

	return workspaceAt(g.wss, path)
}

// order puts the tasks with an agent already open first, since they add no
// session and are the work a resume finds under way; the rest keep the
// drive's order.
func (g *openGate) order(runs []*driveRun) []*driveRun {
	if !g.on {
		return runs
	}

	out := slices.Clone(runs)
	slices.SortStableFunc(out, func(a, b *driveRun) int {
		return cmp.Compare(g.rank(a), g.rank(b))
	})

	return out
}

func (g *openGate) rank(r *driveRun) int {
	if !r.opened && g.workspace(r) != nil {
		return 0
	}

	return 1
}

// admit reports whether r opens now, counting it if so. A task whose agent
// is mid-turn always opens, since the drive cannot stop that turn.
func (g *openGate) admit(in *driveInput, r *driveRun) bool {
	if !g.on {
		return true
	}

	ws := g.workspace(r)
	if ws != nil && ws.Status == agentctl.StatusWorking {
		return true
	}

	g.full = g.full || in.busy(nil) >= g.limit || ws == nil && g.open >= 2*g.limit
	if g.full {
		return false
	}

	if ws == nil {
		g.open++
	}

	return true
}
