package testkit

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/seam"
)

// MemAgents is an agent backend held in memory: what a backend other than
// Herdr sees of start and forward.
type MemAgents struct {
	Spaces  []agentctl.Workspace
	Started []string // agent names
	Args    map[string][]string
	Prompts map[string][]string
	Screens []string          // what Screen answers, in turn, the last one repeated
	Enters  []string          // agents SendEnter pressed Enter in
	Homes   map[string]string // each started agent's workspace id
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
	ws agentctl.Opened,
	name, _ string,
	args []string,
	_ func(waited, limit time.Duration),
) error {
	m.Started = append(m.Started, name)

	if m.Homes == nil {
		m.Homes = map[string]string{}
	}

	m.Homes[name] = ws.ID

	if m.Args == nil {
		m.Args = map[string][]string{}
	}

	m.Args[name] = args

	return nil
}

// MemListing is MemAgents with a listing of its agents, which a backend may
// lack: a discussion's hand-over finds the agent to rename through it.
type MemListing struct{ *MemAgents }

var _ agentctl.Lister = MemListing{}

func (m MemListing) Agents(context.Context) ([]agentctl.AgentInfo, error) {
	out := make([]agentctl.AgentInfo, 0, len(m.Started))
	for _, name := range m.Started {
		out = append(out, agentctl.AgentInfo{Name: name, Workspace: m.Homes[name]})
	}

	return out, nil
}

// UseListingAgents is UseAgents with a backend that lists its agents.
func UseListingAgents(t *testing.T) *MemAgents {
	t.Helper()

	m := &MemAgents{Prompts: map[string][]string{}}
	seam.Swap(t, &agents.Agents, func(string) agentctl.Backend { return MemListing{m} })

	return m
}

var _ agentctl.Renamer = (*MemAgents)(nil)

// RenameAgent renames a started agent, keeping its prompts under the new name.
func (m *MemAgents) RenameAgent(_ context.Context, name, to string) error {
	i := slices.Index(m.Started, name)
	if i < 0 {
		return agentctl.ErrAgentNotFound
	}

	m.Started[i] = to
	if home, ok := m.Homes[name]; ok {
		m.Homes[to] = home
		delete(m.Homes, name)
	}

	if p, ok := m.Prompts[name]; ok {
		m.Prompts[to] = p
		delete(m.Prompts, name)
	}

	return nil
}

func (m *MemAgents) RenameWorkspace(_ context.Context, id, label string) error {
	for i := range m.Spaces {
		if m.Spaces[i].ID == id {
			m.Spaces[i].Label = label

			return nil
		}
	}

	return fmt.Errorf("no workspace %s", id)
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
	seam.Swap(t, &agents.Agents, func(string) agentctl.Backend { return m })

	return m
}
