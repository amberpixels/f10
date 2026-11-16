package api

import (
	"cmp"
	"context"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/printed"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/target"
)

// callerOf is the request's caller context, nil from any listener but the
// unix socket: a remote client has no checkout or session here to pass.
func callerOf(req connect.AnyRequest, c *f10v1.CallerContext) *f10v1.CallerContext {
	if c == nil || !daemon.IsLocal(req) {
		return nil
	}

	return c
}

// withCaller is the request's caller context, and ctx running every command
// the call starts (git, the host and tracker CLIs, Herdr) in the caller's
// own environment, as the command did in process; a remote call keeps the
// daemon's.
func withCaller(
	ctx context.Context, req connect.AnyRequest, c *f10v1.CallerContext,
) (context.Context, *f10v1.CallerContext) {
	c = callerOf(req, c)

	return shell.WithEnv(ctx, c.GetEnv()), c
}

// callerTarget is the checkout a local command runs in when the request
// names no project, read as the command read it in process, its refusals
// worded the same; else the project the request names.
func callerTarget(
	ctx context.Context, sup Supervisor, project string, c *f10v1.CallerContext, verb string,
) (*target.Target, error) {
	if dir := cmp.Or(c.GetCheckout(), c.GetCwd()); project == "" && dir != "" {
		return target.At(ctx, dir, c.GetCheckout() != "", verb)
	}

	return projectTarget(ctx, sup, project, verb)
}

// inSession runs the call's Herdr commands in the caller's session, its
// allowlisted environment, and keeps that session as the newest for the
// calls the daemon makes alone.
func inSession(ctx context.Context, sup Supervisor, c *f10v1.CallerContext, t *target.Target) context.Context {
	if len(c.GetEnv()) == 0 {
		return ctx
	}

	var extra []string
	if t != nil {
		extra = t.Effective.DriveEnv
	}

	env := daemon.DriveEnv(c.GetEnv(), extra)
	sup.Remember(env)

	return agents.WithSession(ctx, env)
}

// output is where a call's command prints, linked as the caller's terminal
// would link it.
func output(c *f10v1.CallerContext) *printed.Output {
	return printed.New(c.GetTerminal())
}

// printedOf is what o holds, for a reply.
func printedOf(o *printed.Output) *f10v1.Printed {
	return &f10v1.Printed{Stdout: o.Stdout.String(), Stderr: o.Stderr.String()}
}
