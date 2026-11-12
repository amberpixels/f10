package testkit

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// MemAgents is an agent backend held in memory: what a backend other than
// Herdr sees of start and forward.
type MemAgents struct {
	Spaces  []agentctl.Workspace
	Started []string // agent names
	Args    map[string][]string
	Prompts map[string][]string
	Screens []string // what Screen answers, in turn, the last one repeated
	Enters  []string // agents SendEnter pressed Enter in
}

func (m *MemAgents) Screen(context.Context, string) (string, error) {
	if len(m.Screens) == 0 {
		return "", nil
	}

	s := m.Screens[0]
	if len(m.Screens) > 1 {
		m.Screens = m.Screens[1:]
	}

	return s, nil
}

func (m *MemAgents) SendEnter(_ context.Context, name string) error {
	m.Enters = append(m.Enters, name)

	return nil
}

func (m *MemAgents) Name() string                        { return "memory" }
func (m *MemAgents) Available() error                    { return nil }
func (m *MemAgents) Focus(context.Context, string) error { return nil }
func (m *MemAgents) Close(context.Context, string) error { return nil }

func (m *MemAgents) Workspaces(context.Context) ([]agentctl.Workspace, error) {
	return slices.Clone(m.Spaces), nil
}

func (m *MemAgents) Open(_ context.Context, path, label string) (agentctl.Opened, []string, error) {
	m.Spaces = append(m.Spaces, agentctl.Workspace{ID: "mem:" + label, Label: label, Path: path})

	return agentctl.Opened{ID: "mem:" + label, Slot: "slot"}, nil, nil
}

func (m *MemAgents) Occupancy(context.Context, agentctl.Opened) (agentctl.Occupancy, error) {
	return agentctl.Free, nil
}

func (m *MemAgents) StartAgent(
	_ context.Context,
	_ agentctl.Opened,
	name, _ string,
	args []string,
	_ func(waited, limit time.Duration),
) error {
	m.Started = append(m.Started, name)

	if m.Args == nil {
		m.Args = map[string][]string{}
	}

	m.Args[name] = args

	return nil
}

func (m *MemAgents) Prompt(_ context.Context, name, text string) error {
	if !slices.Contains(m.Started, name) {
		return agentctl.ErrAgentNotFound
	}

	m.Prompts[name] = append(m.Prompts[name], text)

	return nil
}

func UseAgents(t *testing.T) *MemAgents {
	t.Helper()

	m := &MemAgents{Prompts: map[string][]string{}}
	seam.Swap(t, &worktree.Agents, func(string) agentctl.Backend { return m })

	return m
}
