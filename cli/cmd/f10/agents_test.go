package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

// memAgents is an agent backend held in memory: what a backend other than
// Herdr sees of start and forward.
type memAgents struct {
	workspaces []agentctl.Workspace
	started    []string // agent names
	prompts    map[string][]string
}

func (m *memAgents) Name() string                        { return "memory" }
func (m *memAgents) Available() error                    { return nil }
func (m *memAgents) Focus(context.Context, string) error { return nil }
func (m *memAgents) Close(context.Context, string) error { return nil }

func (m *memAgents) Workspaces(context.Context) ([]agentctl.Workspace, error) {
	return slices.Clone(m.workspaces), nil
}

func (m *memAgents) Open(_ context.Context, path, label string) (agentctl.Opened, []string, error) {
	m.workspaces = append(m.workspaces, agentctl.Workspace{ID: "mem:" + label, Label: label, Path: path})

	return agentctl.Opened{ID: "mem:" + label, Slot: "slot"}, nil, nil
}

func (m *memAgents) Occupancy(context.Context, agentctl.Opened) (agentctl.Occupancy, error) {
	return agentctl.Free, nil
}

func (m *memAgents) StartAgent(
	_ context.Context,
	_ agentctl.Opened,
	name, _ string,
	_ []string,
	_ func(waited, limit time.Duration),
) error {
	m.started = append(m.started, name)

	return nil
}

func (m *memAgents) Prompt(_ context.Context, name, text string) error {
	if !slices.Contains(m.started, name) {
		return agentctl.ErrAgentNotFound
	}

	m.prompts[name] = append(m.prompts[name], text)

	return nil
}

func useAgents(t *testing.T) *memAgents {
	t.Helper()

	m := &memAgents{prompts: map[string][]string{}}
	prev := agents
	agents = func(string) agentctl.Backend { return m }

	t.Cleanup(func() { agents = prev })

	return m
}

// start and forward reach the agent only through the backend: another
// backend gets the workspace, the agent and both prompts, and Herdr is
// never called.
func TestStartAndForwardThroughAnotherBackend(t *testing.T) {
	f := newFakes(t)
	m := useAgents(t)

	f.has["wt"] = true
	f.script(refsLocal, "", heads("GH-1"))
	f.script(worktrees, porcelain(), porcelain("GH-1", fwdWT))
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")

	if err := start(t.Context(), &bytes.Buffer{}, startInput{main: fwdMain, task: gh1}); err != nil {
		t.Fatal(err)
	}

	agent := agentOf("GH-1")
	if !slices.Equal(m.started, []string{agent}) || len(m.prompts[agent]) != 1 {
		t.Fatalf("started %q, prompts %q", m.started, m.prompts)
	}

	m.workspaces[0].Status = agentctl.StatusWorking

	var out bytes.Buffer

	fwd := forwardInput{main: fwdMain, here: fwdMain, task: gh1, text: fwdText, stateDir: t.TempDir(), now: time.Now()}
	if err := forward(t.Context(), &out, fwd); err != nil {
		t.Fatal(err)
	}

	if got := m.prompts[agent]; len(got) != 2 || got[1] != fwdText+" --driven" {
		t.Errorf("prompts = %q", got)
	}

	if !strings.Contains(out.String(), "mem:GH-1") {
		t.Errorf("report lacks the workspace:\n%s", out.String())
	}

	if f.called("herdr") {
		t.Errorf("Herdr was called: %q", f.calls)
	}
}

// A bare-named agent, started before names carried the path hash, still
// gets the prompt: the backend's not-found is what turns to the bare name.
func TestPromptAgentFallsBackToTheBareName(t *testing.T) {
	m := useAgents(t)
	m.started = []string{agentctl.BareAgentName("GH-1", "")}

	if err := promptAgent(t.Context(), fwdWT, "GH-1", "", "hi"); err != nil {
		t.Fatal(err)
	}

	if err := promptAgent(t.Context(), fwdWT, "GH-2", "", "hi"); !errors.Is(err, agentctl.ErrAgentNotFound) {
		t.Errorf("unknown agent = %v, want ErrAgentNotFound", err)
	}
}
