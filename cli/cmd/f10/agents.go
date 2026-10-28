package main

import (
	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/herdr"
)

// agents is the backend hosting task agents, answering from dir: Herdr,
// the only one. A variable so a test can host agents without it.
var agents = func(dir string) agentctl.Backend { return herdr.New(dir) }

// reachableAgents is the backend answering from dir, or nil when this
// process cannot reach it.
func reachableAgents(dir string) agentctl.Backend {
	b := agents(dir)
	if b.Available() != nil {
		return nil
	}

	return b
}
