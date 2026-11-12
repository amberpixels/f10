package start_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// start and forward reach the agent only through the backend: another
// backend gets the workspace, the agent and both prompts, the first on the
// agent's command line and the forward typed, and Herdr is never called.
func TestStartAndForwardThroughAnotherBackend(t *testing.T) {
	f := testkit.NewFakes(t)
	m := testkit.UseAgents(t)

	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, "", testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", testkit.FwdWT))
	f.Script("wt switch --no-cd --yes --format json --create GH-1", "{}")

	if err := start.Start(
		t.Context(),
		&bytes.Buffer{},
		start.StartInput{Main: testkit.FwdMain, Storage: testkit.Storage, Task: testkit.Gh1},
	); err != nil {
		t.Fatal(err)
	}

	agent := testkit.AgentOf("GH-1")
	if !slices.Equal(m.Started, []string{agent}) || len(m.Prompts[agent]) != 0 ||
		len(m.Args[agent]) == 0 || !strings.HasPrefix(m.Args[agent][0], "/f10:") {
		t.Fatalf("started %q with %q, prompts %q", m.Started, m.Args, m.Prompts)
	}

	m.Spaces[0].Status = agentctl.StatusWorking

	var out bytes.Buffer

	fwd := startcmd.ForwardInput{
		Main:     testkit.FwdMain,
		Here:     testkit.FwdMain,
		Task:     testkit.Gh1,
		Text:     testkit.FwdText,
		StateDir: t.TempDir(),
		Now:      time.Now(),
	}
	if err := startcmd.Forward(t.Context(), &out, fwd); err != nil {
		t.Fatal(err)
	}

	if got := m.Prompts[agent]; len(got) != 1 || got[0] != testkit.FwdText+" --driven" {
		t.Errorf("prompts = %q", got)
	}

	if !strings.Contains(out.String(), "mem:GH-1") {
		t.Errorf("report lacks the workspace:\n%s", out.String())
	}

	if f.Called("herdr") {
		t.Errorf("Herdr was called: %q", f.Calls)
	}
}
