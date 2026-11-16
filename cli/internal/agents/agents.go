// Package agents is the generic agents core: the backend that hosts task
// agents, and the asks they wait on. It is the only package that talks to
// Herdr, so the rest of f10 stays separable from it.
package agents

import (
	"context"
	"errors"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/herdr"
)

// SessionEnv is the environment Herdr calls run under, nil for this
// process's own. The daemon sets it to the newest local session's, because
// it outlives the client that spawned it; with none registered, Herdr is
// unreachable rather than reached through a session that may be gone.
var SessionEnv func() []string

// Agents is the backend hosting task agents, answering from dir: Herdr,
// the only one. A variable so a test can host agents without it.
var Agents = func(dir string) agentctl.Backend {
	b := herdr.New(dir)
	if SessionEnv == nil {
		return b
	}

	if b.Env = SessionEnv(); len(b.Env) == 0 {
		return unreachable{b}
	}

	return b
}

// ErrNoSession is the daemon's refusal of every agent call while no local
// client has registered the Herdr session it runs in.
var ErrNoSession = errors.New(
	"no Herdr session has registered with the daemon: run an f10 command inside Herdr")

// unreachable is a backend with no session: every call refuses, so none
// runs herdr with an environment that cannot reach it.
type unreachable struct{ herdr.Backend }

func (unreachable) Available() error { return noSessionError{} }

func (unreachable) Workspaces(context.Context) ([]agentctl.Workspace, error) {
	return nil, noSessionError{}
}

func (unreachable) Open(context.Context, string, string) (agentctl.Opened, []string, error) {
	return agentctl.Opened{}, nil, noSessionError{}
}

func (unreachable) Occupancy(context.Context, agentctl.Opened) (agentctl.Occupancy, error) {
	return agentctl.Busy, noSessionError{}
}

func (unreachable) StartAgent(
	context.Context, agentctl.Opened, string, string, []string, func(waited, limit time.Duration),
) error {
	return noSessionError{}
}

func (unreachable) Prompt(context.Context, string, string) error { return noSessionError{} }

func (unreachable) Screen(context.Context, string) (string, error) { return "", noSessionError{} }

func (unreachable) SendEnter(context.Context, string) error { return noSessionError{} }

func (unreachable) Focus(context.Context, string) error { return noSessionError{} }

func (unreachable) Close(context.Context, string) error { return noSessionError{} }

func (unreachable) Agents(context.Context) ([]agentctl.AgentInfo, error) {
	return nil, noSessionError{}
}

// noSessionError words ErrNoSession and still matches herdr's own refusal, so
// every check for a process outside Herdr reads it.
type noSessionError struct{}

func (noSessionError) Error() string { return ErrNoSession.Error() }

func (noSessionError) Is(target error) bool {
	return target == ErrNoSession || target == herdr.ErrNotInside
}

// ReachableAgents is the backend answering from dir, or nil when this
// process cannot reach it.
func ReachableAgents(dir string) agentctl.Backend {
	b := Agents(dir)
	if b.Available() != nil {
		return nil
	}

	return b
}

// RequireAgents is the check start, forward, drive and finish make before
// anything else: they run inside a Herdr session and nowhere else, with no
// fallback by design.
func RequireAgents() error {
	err := Agents("").Available()
	if errors.Is(err, herdr.ErrNotInside) {
		return outsideHerdrError{err}
	}

	return err
}

// RequireAgentsIn is RequireAgents for a client whose environment is env,
// which the daemon checks for it; a nil env checks this process's own.
func RequireAgentsIn(env []string) error {
	if env == nil {
		return RequireAgents()
	}

	err := herdr.Backend{Env: env}.Available()
	if errors.Is(err, herdr.ErrNotInside) {
		return outsideHerdrError{err}
	}

	return err
}

// WithSession runs the agent calls made under ctx in env's session
// (herdr.WithSession), for a daemon calling for one client of several.
func WithSession(ctx context.Context, env []string) context.Context {
	return herdr.WithSession(ctx, env)
}

// ErrNotInside is the refusal of a process outside Herdr, which IsOutside
// reports.
var ErrNotInside = herdr.ErrNotInside

// IsOutside reports err as the refusal of a process outside Herdr.
func IsOutside(err error) bool { return errors.Is(err, herdr.ErrNotInside) }

// outsideHerdrError words herdr's refusal for the commands that need it, and
// still unwraps to herdr.ErrNotInside.
type outsideHerdrError struct{ err error }

func (outsideHerdrError) Error() string {
	return "f10 start, forward, drive and finish run inside a Herdr session: " +
		"open a pane there, or install it from https://herdr.dev"
}

func (e outsideHerdrError) Unwrap() error { return e.err }
