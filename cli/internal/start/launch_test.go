package start

import (
	"bytes"
	"testing"

	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A first prompt herdr refuses as an argument, one with a newline, is typed
// once the agent starts, and its input box is checked after.
func TestStartTypesAPromptWithANewline(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")

	text := launch.Prompt(launch.ModeDiscuss, runplan.ShipFlow{}, "GH-1", nil, "", nil, "first\nsecond")
	f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+text, "{}")

	in := StartInput{
		Main:    "/code/repo",
		Storage: testkit.Storage,
		Task:    testkit.Gh1,
		Mode:    launch.ModeDiscuss,
		Note:    "first\nsecond",
	}
	if err := Start(t.Context(), &bytes.Buffer{}, in); err != nil {
		t.Fatal(err)
	}

	if !f.Called("herdr agent prompt") || !f.Called("herdr agent read "+testkit.AgentOf("GH-1")) {
		t.Errorf("the prompt was not typed and checked: %q", f.Calls)
	}
}
