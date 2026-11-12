package drive

import (
	"cmp"
	"context"
	"fmt"
	"runtime"
	"slices"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// DefaultParallel is a drive's cap with no --parallel: half the cores, since
// each agent's verify uses several, held between 2 and 8.
func DefaultParallel() int { return min(max(runtime.NumCPU()/2, 2), 8) }

// HoldsSlot: a run on its way keeps its slot from one skill to the next;
// one parked, waiting or queued has given it up.
func (r *DriveRun) HoldsSlot() bool {
	return r.active() && r.Phase != atParked && r.Phase != atWaiting && r.Phase != atQueued
}

// SlotFree reports whether r may send a skill under the cap. A turn already
// running in its workspace takes a slot past the cap: the drive cannot stop it.
func (in *DriveInput) SlotFree(r *DriveRun) bool {
	if in.Parallel <= 0 || in.Busy(r) < in.Parallel {
		return true
	}

	ws := worktree.WorkspaceByID(in.Wss, r.O.Ws.ID)

	return ws != nil && ws.Status == agentctl.StatusWorking
}

// Busy counts the runs holding a slot, except.
func (in *DriveInput) Busy(except *DriveRun) int {
	n := 0

	for _, r := range in.Runs {
		if r != except && r.HoldsSlot() {
			n++
		}
	}

	return n
}

func (in *DriveInput) Queue(r *DriveRun) {
	if r.Queued() {
		return
	}

	held := AtHeld
	if r.Opened() {
		held = atQueued
	}

	in.Enter(r, held)
	fmt.Fprintf(in.Out, "%s: queued, %d running\n", in.id(r.Task.Ref.ID), in.Busy(r))
}

// Unqueue releases r from the cap: an opened task runs its next skill, one
// not opened yet is pending until open.
func (in *DriveInput) Unqueue(r *DriveRun) {
	switch r.Phase {
	case AtHeld:
		in.Enter(r, atPending)
	case atQueued:
		in.Enter(r, atRunning)
	case atPending, atRunning, atVerifying, atRetrying, atParked, atWaiting:
	}
}

// An openGate decides, for one startRunnable pass, which released tasks
// open under the cap. The zero gate admits every task.
type openGate struct {
	On    bool
	Limit int
	Wss   []agentctl.Workspace
	Trees map[string]string // a task's branch to its checkout, for a task not opened yet
	Open  int               // active and asking tasks with a workspace, this pass's opens included
	full  bool              // a task queued: every later one queues behind it
}

// Workspace is the one showing r's checkout, nil for none.
func (g *openGate) Workspace(r *DriveRun) *agentctl.Workspace {
	path := r.O.Path
	if !r.Opened() && r.Task.Branch != "" {
		path = g.Trees[r.Task.Branch]
	}

	if path == "" {
		return nil
	}

	return worktree.WorkspaceAt(g.Wss, path)
}

// Order puts the tasks with an agent already open first, since they add no
// session and are the work a resume finds under way; the rest keep the
// drive's order.
func (g *openGate) Order(runs []*DriveRun) []*DriveRun {
	if !g.On {
		return runs
	}

	out := slices.Clone(runs)
	slices.SortStableFunc(out, func(a, b *DriveRun) int {
		return cmp.Compare(g.rank(a), g.rank(b))
	})

	return out
}

// Admit reports whether r opens now, counting it if so. A task whose agent
// is mid-turn always opens, since the drive cannot stop that turn.
func (g *openGate) Admit(in *DriveInput, r *DriveRun) bool {
	if !g.On {
		return true
	}

	ws := g.Workspace(r)
	if ws != nil && ws.Status == agentctl.StatusWorking {
		return true
	}

	g.full = g.full || in.Busy(nil) >= g.Limit || ws == nil && g.Open >= 2*g.Limit
	if g.full {
		return false
	}

	if ws == nil {
		g.Open++
	}

	return true
}

func (g *openGate) rank(r *DriveRun) int {
	if !r.Opened() && g.Workspace(r) != nil {
		return 0
	}

	return 1
}

// openGate reads the workspaces and checkouts only when the cap can bind:
// a drive no longer than its cap never reaches it, nor one with no task
// left to open.
//
//nolint:lostfield // a gate reads the runs and workspaces, not every field of them
//nolint:lostfield // a gate reads the runs and workspaces, not every field of them
func (in *DriveInput) openGate(ctx context.Context, runs []*DriveRun, wss []agentctl.Workspace) (*openGate, error) {
	unopened := slices.ContainsFunc(runs, func(r *DriveRun) bool { return !r.Opened() && r.End == nil })
	if in.Parallel <= 0 || len(runs) <= in.Parallel || !unopened {
		return &openGate{}, nil
	}

	if wss == nil {
		var err error
		if wss, err = worktree.Agents(in.Main).Workspaces(ctx); err != nil {
			return nil, err
		}

		in.Wss = wss
	}

	g := &openGate{On: true, Limit: in.Parallel, Wss: wss, Trees: map[string]string{}}

	// only an unopened task with a branch is found by its checkout
	if slices.ContainsFunc(runs, func(r *DriveRun) bool { return !r.Opened() && r.End == nil && r.Task.Branch != "" }) {
		trees, err := gitx.Worktrees(ctx, in.Main)
		if err != nil {
			return nil, err
		}

		for _, t := range trees {
			if t.Branch != "" {
				g.Trees[t.Branch] = t.Path
			}
		}
	}

	// an earlier run of this drive may have left agents open, and they
	// count; a done or halted task's agent is the user's now, and counting
	// it would stop a drive without finish with tasks left
	for _, r := range runs {
		if (r.End == nil || r.End.Code == ui.ExitAsk) && g.Workspace(r) != nil {
			g.Open++
		}
	}

	return g, nil
}
