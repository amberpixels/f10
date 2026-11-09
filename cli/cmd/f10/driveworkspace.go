package main

import (
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/state"
)

func (in *driveInput) prTarget(ctx context.Context, r *driveRun) (prTarget, error) {
	return prTargetOf(ctx, in.main, r.o.branch)
}

// look reads the workspace by the id open settled on, since a checkout can
// show in more than one; one gone mid-run took the agent with it.
func (in *driveInput) look(ctx context.Context, r *driveRun) (*agentctl.Workspace, *state.Run, error) {
	wss, err := agents(in.main).Workspaces(ctx)
	if err != nil {
		return nil, nil, err
	}

	ws := workspaceByID(wss, r.o.ws.ID)
	if ws == nil {
		return nil, nil, in.workspaceGone(r)
	}

	run, err := in.run(r.o.path)

	return ws, run, err
}

func (in *driveInput) workspaceGone(r *driveRun) error {
	return fmt.Errorf("%s no longer shows workspace %s, which showed %s", agents(in.main).Name(), r.o.ws.ID, r.o.path)
}

func workspaceName(ws *agentctl.Workspace) string {
	if ws.Label == "" || ws.Label == ws.ID {
		return ws.ID
	}

	return ws.Label + " (" + ws.ID + ")"
}

// lookAll is one tick's view, read once for every task in flight.
func (in *driveInput) lookAll(ctx context.Context) ([]agentctl.Workspace, []*state.Run, error) {
	wss, err := agents(in.main).Workspaces(ctx)
	if err != nil {
		return nil, nil, err
	}

	live, err := state.List(in.stateDir, in.now(), in.ttl)
	if err != nil {
		return nil, nil, err
	}

	return wss, live, nil
}

func (in *driveInput) run(path string) (*state.Run, error) {
	live, err := state.List(in.stateDir, in.now(), in.ttl)
	if err != nil {
		return nil, err
	}

	if runs := inRoot(live, path); len(runs) > 0 {
		return runs[0], nil
	}

	return nil, nil
}
