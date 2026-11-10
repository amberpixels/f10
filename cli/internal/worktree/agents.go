package worktree

import (
	"errors"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/herdr"
)

// Agents is the backend hosting task agents, answering from dir: Herdr,
// the only one. A variable so a test can host agents without it.
var Agents = func(dir string) agentctl.Backend { return herdr.New(dir) }

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
		return outsideHerdr{err}
	}

	return err
}

// outsideHerdr words herdr's refusal for the commands that need it, and
// still unwraps to herdr.ErrNotInside.
type outsideHerdr struct{ err error }

func (outsideHerdr) Error() string {
	return "f10 start, forward, drive and finish run inside a Herdr session: " +
		"open a pane there, or install it from https://herdr.dev"
}

func (e outsideHerdr) Unwrap() error { return e.err }
