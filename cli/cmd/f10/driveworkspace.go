package main

import (
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

func (in *driveInput) prTarget(ctx context.Context, r *driveRun) (stack.PRTarget, error) {
	return stack.PRTargetOf(ctx, in.main, r.o.Branch)
}

// look reads the workspace by the id open settled on, since a checkout can
// show in more than one; one gone mid-run took the agent with it.
func (in *driveInput) look(ctx context.Context, r *driveRun) (*agentctl.Workspace, *state.Run, error) {
	// a task opened this tick is not in the tick's look
	if ws := finish.WorkspaceByID(in.view().wss, r.o.Ws.ID); ws != nil && !in.view().opened[r] {
		run, err := in.run(r.o.Path)

		return ws, run, err
	}

	wss, err := worktree.Agents(in.main).Workspaces(ctx)
	if err != nil {
		return nil, nil, err
	}

	ws := finish.WorkspaceByID(wss, r.o.Ws.ID)
	if ws == nil {
		return nil, nil, in.workspaceGone(r)
	}

	run, err := in.runNow(r.o.Path)

	return ws, run, err
}

func (in *driveInput) workspaceGone(r *driveRun) error {
	return fmt.Errorf(
		"%s no longer shows workspace %s, which showed %s",
		worktree.Agents(in.main).Name(),
		r.o.Ws.ID,
		r.o.Path,
	)
}

func workspaceName(ws *agentctl.Workspace) string {
	if ws.Label == "" || ws.Label == ws.ID {
		return ws.ID
	}

	return ws.Label + " (" + ws.ID + ")"
}

// A tickView is what one tick reads once for every task in flight: the
// workspaces, the live runs, and one fetch of origin.
type tickView struct {
	wss    []agentctl.Workspace // nil before the first tick's look
	live   []*state.Run
	fetch  stack.FetchOnce
	opened map[*driveRun]bool // the tasks opened since the look, which it does not show
}

// lookAll is one tick's view.
func (in *driveInput) lookAll(ctx context.Context) (*tickView, error) {
	wss, err := worktree.Agents(in.main).Workspaces(ctx)
	if err != nil {
		return nil, err
	}

	live, err := state.List(in.stateDir, in.now(), in.ttl)
	if err != nil {
		return nil, err
	}

	return &tickView{wss: wss, live: live}, nil
}

// run is path's live run as the tick read it, else read now.
func (in *driveInput) run(path string) (*state.Run, error) {
	if in.view().wss == nil {
		return in.runNow(path)
	}

	if runs := statusview.InRoot(in.view().live, path); len(runs) > 0 {
		return runs[0], nil
	}

	return nil, nil
}

func (in *driveInput) runNow(path string) (*state.Run, error) {
	live, err := state.List(in.stateDir, in.now(), in.ttl)
	if err != nil {
		return nil, err
	}

	if runs := statusview.InRoot(live, path); len(runs) > 0 {
		return runs[0], nil
	}

	return nil, nil
}

// stallCheck reads r's run state as of its last send, ignoring runs from
// before the drive opened the task.
func (in *driveInput) stallCheck(r *driveRun) start.StallCheck {
	return start.StallCheck{StateDir: in.stateDir, Ttl: in.ttl, Now: r.sentAt, Since: r.freshAt}
}

// view is this tick's look, or before the first tick an empty one that
// still keeps the fetch.
func (in *driveInput) view() *tickView {
	if in.tick == nil {
		in.tick = &tickView{}
	}

	return in.tick
}

func (v *tickView) justOpened(r *driveRun) {
	if v.opened == nil {
		v.opened = map[*driveRun]bool{}
	}

	v.opened[r] = true
}
