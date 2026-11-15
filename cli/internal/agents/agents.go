// Package agents is the generic agents core: the backend that hosts task
// agents, and the asks they wait on. It is the only package that talks to
// Herdr, so the rest of f10 stays separable from it.
package agents

import (
	"errors"

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
	if SessionEnv != nil {
		b.Env = append([]string{}, SessionEnv()...)
	}

	return b
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
