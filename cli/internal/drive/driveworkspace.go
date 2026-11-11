package drive

import (
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

func (in *DriveInput) prTarget(ctx context.Context, r *DriveRun) (stack.PRTarget, error) {
	return stack.PRTargetOf(ctx, in.Main, r.O.Branch)
}

// Look reads the workspace by the id open settled on, since a checkout can
// show in more than one; one gone mid-run took the agent with it.
func (in *DriveInput) Look(ctx context.Context, r *DriveRun) (*agentctl.Workspace, *state.Run, error) {
	// a task opened this tick is not in the tick's look
	if ws := worktree.WorkspaceByID(in.View().Wss, r.O.Ws.ID); ws != nil && !in.View().opened[r] {
		run, err := in.run(r.O.Path)

		return ws, run, err
	}

	wss, err := worktree.Agents(in.Main).Workspaces(ctx)
	if err != nil {
		return nil, nil, err
	}

	ws := worktree.WorkspaceByID(wss, r.O.Ws.ID)
	if ws == nil {
		return nil, nil, in.workspaceGone(r)
	}

	run, err := in.runNow(r.O.Path)

	return ws, run, err
}

func (in *DriveInput) workspaceGone(r *DriveRun) error {
	return fmt.Errorf(
		"%s no longer shows workspace %s, which showed %s",
		worktree.Agents(in.Main).Name(),
		r.O.Ws.ID,
		r.O.Path,
	)
}

func workspaceName(ws *agentctl.Workspace) string {
	if ws.Label == "" || ws.Label == ws.ID {
		return ws.ID
	}

	return ws.Label + " (" + ws.ID + ")"
}

// A TickView is what one tick reads once for every task in flight: the
// workspaces, the live runs, and one fetch of origin.
type TickView struct {
	Wss    []agentctl.Workspace // nil before the first tick's look
	live   []*state.Run
	fetch  stack.FetchOnce
	opened map[*DriveRun]bool // the tasks opened since the look, which it does not show
}

func (v *TickView) JustOpened(r *DriveRun) {
	if v.opened == nil {
		v.opened = map[*DriveRun]bool{}
	}

	v.opened[r] = true
}

// lookAll is one tick's view.
func (in *DriveInput) lookAll(ctx context.Context) (*TickView, error) {
	wss, err := worktree.Agents(in.Main).Workspaces(ctx)
	if err != nil {
		return nil, err
	}

	live, err := state.List(in.StateDir, in.Now(), in.TTL)
	if err != nil {
		return nil, err
	}

	return &TickView{Wss: wss, live: live}, nil
}

// run is path's live run as the tick read it, else read now.
func (in *DriveInput) run(path string) (*state.Run, error) {
	if in.View().Wss == nil {
		return in.runNow(path)
	}

	if runs := statusview.InRoot(in.View().live, path); len(runs) > 0 {
		return runs[0], nil
	}

	return nil, nil
}

func (in *DriveInput) runNow(path string) (*state.Run, error) {
	live, err := state.List(in.StateDir, in.Now(), in.TTL)
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
func (in *DriveInput) stallCheck(r *DriveRun) start.StallCheck {
	return start.StallCheck{StateDir: in.StateDir, Ttl: in.TTL, Now: r.SentAt, Since: r.freshAt}
}

// View is this tick's look, or before the first tick an empty one that
// still keeps the fetch.
func (in *DriveInput) View() *TickView {
	if in.Tick == nil {
		in.Tick = &TickView{}
	}

	return in.Tick
}
