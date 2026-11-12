package start_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// f10 start --pace records the pace on the new branch, and a pace that
// skips plan sends ship alone.
func TestStartRecordsThePace(t *testing.T) {
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": flow.Of(strings.Fields("plan judge implement pr"))}}

	ultra, err := testkit.PacedFlowIn(eff, facts.NewPaceSet(), "ultra", "")
	if err != nil {
		t.Fatal(err)
	}

	if got := launch.Prompt(startcmd.ModeDefault, ultra, "GH-1", nil, "", nil, ""); strings.Contains(got, "/f10:plan") {
		t.Errorf("ultra's prompt = %q, want ship alone", got)
	}

	f := testkit.NewFakes(t)
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.RefsAfter, testkit.Heads("GH-7/base"))
	f.Script(testkit.ShaAfter, "ccc")
	f.Script(testkit.HeadRef, "origin/main")
	f.Script(testkit.ShaMain, "bbb")
	f.Script("git merge-base refs/heads/GH-1 refs/heads/GH-7/base", "ccc")

	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("git worktree add -b GH-1 /code/repo.GH-1 GH-7/base", "")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")

	dep := &launch.Dependency{Task: "GH-7", Branch: "GH-7/base", Plan: testkit.Storage.Plan("GH-7")}
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
		)+" "+launch.Prompt(
			startcmd.ModeDefault,
			ultra,
			"GH-1",
			dep,
			"",
			nil,
			"",
		),
		"{}",
	)

	var out bytes.Buffer

	in := start.StartInput{
		Main:    "/code/repo",
		Storage: testkit.Storage,
		Task:    testkit.Gh1,
		Flow:    ultra,
		Pace:    "ultra",
		After:   "GH-7",
	}
	if err := start.Start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	if !f.Called("set branch.GH-1." + string(branchmeta.Pace) + " ultra") {
		t.Errorf("the pace was not recorded on the branch: %v", f.Calls)
	}
}
