package start_test

import (
	"bytes"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A fresh start hands the agent its first prompt on its command line, ahead
// of the profile's flags, and never types it into the pane.
func TestStartLaunchesWithTheFirstPrompt(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)

	launched := "herdr agent start " + testkit.AgentOf("GH-1") + " --kind claude --pane pane:7 -- " +
		launch.Prompt(startcmd.ModeDefault, runplan.ShipFlow{}, "GH-1", nil, "", nil, "") + " --effort high"
	f.Script(launched, "{}")

	in := start.StartInput{
		Main:           "/code/repo",
		Storage:        testkit.Storage,
		Task:           testkit.Gh1,
		Profile:        agentctl.Profile{Effort: "high"},
		ProfileFlagged: true,
	}
	if err := start.Start(t.Context(), &bytes.Buffer{}, in); err != nil {
		t.Fatal(err)
	}

	if !f.Called(launched) {
		t.Errorf("the agent was not launched with its prompt: %q", f.Calls)
	}

	if f.Called("herdr agent prompt") || f.Called("herdr agent read") {
		t.Errorf("the first prompt was typed: %q", f.Calls)
	}
}
