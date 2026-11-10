package worktree

import (
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
