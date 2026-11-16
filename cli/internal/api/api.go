// Package api serves f10's control plane as Connect services: the f10.v1
// handlers over the core packages, and the agents.v1 ones from the agents
// core, on one mux the daemon mounts on its socket.
package api

import (
	"context"
	"net/http"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/agents/agentsrpc"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// Supervisor is what the handlers need of the daemon: who it is, the drives
// and watches it runs, and the session environment Herdr calls run under,
// which a local caller's session replaces.
type Supervisor interface {
	Status() daemon.Status
	WatchProject(root, dir string, env []string) (string, error)
	RunDrive(spec daemon.DriveSpec) (daemon.Started, error)
	ResumeDrive(root, dir string, env []string, n int) (daemon.Started, error)
	SessionEnv() []string
	Remember(env []string)
}

// Handler is every service on one mux, over sup.
func Handler(sup Supervisor) http.Handler {
	mux := http.NewServeMux()
	opts := errorOptions()

	mux.Handle(f10v1connect.NewRunServiceHandler(&runService{sup: sup}, opts...))
	mux.Handle(f10v1connect.NewTaskServiceHandler(&taskService{sup: sup}, opts...))
	mux.Handle(f10v1connect.NewBoardServiceHandler(&boardService{sup: sup}, opts...))
	mux.Handle(f10v1connect.NewEventServiceHandler(&eventService{}, opts...))
	mux.Handle(f10v1connect.NewArtifactServiceHandler(&artifactService{sup: sup}, opts...))
	mux.Handle(f10v1connect.NewProjectServiceHandler(&projectService{sup: sup}, opts...))

	agentsrpc.Mount(mux, agentsrpc.Options{
		Deliverer: deliverer(sup),
		Session: func(ctx context.Context, env []string) context.Context {
			return inSession(shell.WithEnv(ctx, env), sup, &f10v1.CallerContext{Env: env}, nil)
		},
		Links: askLinks,
	}, opts...)

	return mux
}
