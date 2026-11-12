package start_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A picked-up PR is summarised and discussed with no driven marker, or
// planned from its diff with --plan; never shipped.
func TestPromptForAPickup(t *testing.T) {
	for _, mode := range []string{startcmd.ModeDefault, launch.ModeDiscuss} {
		got := launch.Prompt(mode, runplan.ShipFlow{}, "GH-1", nil, "", testkit.PickGH, "")
		for _, want := range []string{"f10 task read GH-1", "f10 pr view --comments 30", "f10 pr diff 30", "stop and wait"} {
			if !strings.Contains(got, want) {
				t.Errorf("prompt(%q) for a PR = %q, missing %q", mode, got, want)
			}
		}

		if strings.Contains(got, launch.DrivenMarker) || strings.Contains(got, "/f10:ship") {
			t.Errorf("prompt(%q) for a PR = %q, want plain text with no ship", mode, got)
		}
	}

	want := "/f10:plan GH-1 continue PR " + testkit.PickGH.URL + ": its diff is the starting point " + launch.DrivenMarker
	if got := launch.Prompt(launch.ModePlan, runplan.ShipFlow{}, "GH-1", nil, "", testkit.PickGH, ""); got != want {
		t.Errorf("prompt(plan) for a PR = %q, want %q", got, want)
	}

	if got := launch.Prompt(
		startcmd.ModeDefault,
		runplan.ShipFlow{},
		"GH-1",
		nil,
		"/p/GH-1.md",
		testkit.PickGH,
		"",
	); !strings.Contains(
		got,
		"/p/GH-1.md",
	) {
		t.Errorf("prompt for a PR with a plan = %q, want the plan named", got)
	}

	got := launch.Prompt(startcmd.ModeDefault, runplan.ShipFlow{}, "GH-1", nil, "", testkit.PickMerged, "")
	if !strings.Contains(got, "that the PR is merged by alice on 2026-10-12 and what its diff does") ||
		strings.Contains(got, "open threads") {
		t.Errorf("prompt for a merged PR = %q, want its state named before the diff", got)
	}
}

// --plan on a landed PR is refused: going on from it is a task of its own.
func TestRefuseLandedPlan(t *testing.T) {
	err := startcmd.RefuseLandedPlan(launch.ModePlan, testkit.PickMerged)
	if err == nil || !strings.Contains(err.Error(), "merged by alice") {
		t.Errorf("--plan on a merged PR = %v, want a refusal naming its state", err)
	}

	for _, c := range []struct {
		mode string
		pick *hostops.Pickup
	}{{startcmd.ModeDefault, testkit.PickMerged}, {launch.ModeDiscuss, testkit.PickMerged}, {launch.ModePlan, testkit.PickGH}, {launch.ModePlan, nil}} {
		if err := startcmd.RefuseLandedPlan(c.mode, c.pick); err != nil {
			t.Errorf("refuseLandedPlan(%q, %v) = %v, want nil", c.mode, c.pick, err)
		}
	}
}

func TestRefusePickupFlags(t *testing.T) {
	if err := startcmd.RefusePickupFlags("", "", ""); err != nil {
		t.Errorf("a PR with no flow named was refused: %v", err)
	}

	for _, c := range [][3]string{{"local", "", ""}, {"direct", "", ""}, {"", "GH-7", ""}, {"", "", "main"}} {
		if err := startcmd.RefusePickupFlags(c[0], c[1], c[2]); err == nil {
			t.Errorf("refusePickupFlags%v = nil, want a refusal", c)
		}
	}
}

// The PR's branch exists only on origin: start creates the local branch
// tracking it, checks it out, links it to the task, and prompts the summary.
func TestStartPicksUpAPR(t *testing.T) {
	const wt = "/code/repo.alice-fix-login"

	f := testkit.NewFakes(t)
	f.Fail("git rev-parse --verify --quiet refs/heads/alice/fix-login", testkit.Missing)
	f.Script("git branch --track alice/fix-login origin/alice/fix-login", "")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("alice/fix-login", wt))
	f.Script("git worktree add "+wt+" alice/fix-login", "")

	f.Script("herdr worktree open --cwd /code/repo --path "+wt+" --label alice/fix-login", testkit.Opened)
	f.Script("herdr agent start "+agentctl.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+agentctl.AgentName(
			"GH-1",
			"",
			wt,
		)+" "+launch.Prompt(
			startcmd.ModeDefault,
			runplan.ShipFlow{},
			"GH-1",
			nil,
			"",
			testkit.PickGH,
			"",
		),
		"{}",
	)

	var out bytes.Buffer

	if err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, Pick: testkit.PickGH},
	); err != nil {
		t.Fatal(err)
	}

	tracked := testkit.IndexAfter(f.Calls, "git branch --track alice/fix-login origin/alice/fix-login", -1)
	if tracked < 0 || testkit.IndexAfter(f.Calls, "git worktree add "+wt+" alice/fix-login", tracked) < 0 {
		t.Errorf("the tracking branch was not created before its worktree: %v", f.Calls)
	}

	if !f.Called("set branch.alice/fix-login.f10-task GH-1") {
		t.Errorf("the branch was not linked to its task: %v", f.Calls)
	}

	if f.Called("git for-each-ref") || f.Called("git symbolic-ref") {
		t.Errorf("a picked-up PR looked for a task branch or a base: %v", f.Calls)
	}

	for _, want := range []string{"pr:", testkit.PickGH.URL, "branch:", "alice/fix-login", "tracking origin/alice/fix-login"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// A merged PR is checked out at its merge commit on the task's own branch,
// tracking nothing: origin's branches are never looked at, the branch is
// handed back from the watcher, and the agent is told the PR's state.
func TestStartPicksUpAMergedPR(t *testing.T) {
	const wt = "/code/repo.GH-1-fix-login"

	f := testkit.NewFakes(t)
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1/fix-login", wt))
	f.Script("git worktree add -b GH-1/fix-login "+wt+" abc1234abc", "")

	f.Script("herdr worktree open --cwd /code/repo --path "+wt+" --label GH-1/fix-login", testkit.Opened)
	f.Script("herdr agent start "+agentctl.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+agentctl.AgentName("GH-1", "", wt)+" "+
			launch.Prompt(startcmd.ModeDefault, runplan.ShipFlow{}, "GH-1", nil, "", testkit.PickMerged, ""),
		"{}",
	)

	var out bytes.Buffer

	in := start.StartInput{
		Main:    "/code/repo",
		Storage: testkit.Storage,
		Task:    testkit.Gh1,
		Pick:    testkit.PickMerged,
		Title:   testkit.Titled("Fix login", nil),
	}
	if err := start.Start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	for _, never := range []string{"git branch --track", "git merge-base", "git symbolic-ref", testkit.RefsOrigin} {
		if f.Called(never) {
			t.Errorf("a merged PR ran %q: %v", never, f.Calls)
		}
	}

	if !f.Called("set branch.GH-1/fix-login.f10-watch false") {
		t.Errorf("the branch was not handed back from the watcher: %v", f.Calls)
	}

	for _, want := range []string{
		"pr:", testkit.PickGH.URL, "branch:", "GH-1/fix-login",
		"merged by alice on 2026-10-12: GH-1/fix-login is local only at abc1234",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// A branch the task already has here is reused as it is, the landed
// commit noted as ignored, and left watched: its own work may be live.
func TestStartPicksUpAMergedPROnItsOwnBranch(t *testing.T) {
	const wt = "/code/repo.GH-1-fix-login"

	f := testkit.NewFakes(t)
	f.Meta.Seed("GH-1/fix-login", branchmeta.Task, "GH-1")
	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1/fix-login", wt))

	f.Script("herdr worktree open --cwd /code/repo --path "+wt+" --label GH-1/fix-login", testkit.Opened)
	f.Script("herdr agent start "+agentctl.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+agentctl.AgentName("GH-1", "", wt)+" "+
			launch.Prompt(startcmd.ModeDefault, runplan.ShipFlow{}, "GH-1", nil, "", testkit.PickMerged, ""),
		"{}",
	)

	var out bytes.Buffer

	in := start.StartInput{
		Main:    "/code/repo",
		Storage: testkit.Storage,
		Task:    testkit.Gh1,
		Pick:    testkit.PickMerged,
		Title:   testkit.Titled("Fix login", nil),
	}
	if err := start.Start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	if f.Called("git worktree add") || f.Called("set branch.GH-1/fix-login.f10-watch") {
		t.Errorf("the task's own worktree was not reused as it is: %v", f.Calls)
	}

	for _, want := range []string{"reused the existing worktree", "landed commit ignored", "stays watched"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// Plain `start <id>` whose branch only origin has tracks it rather than
// creating a second branch from main.
func TestStartTracksAnOriginOnlyBranch(t *testing.T) {
	const wt = "/code/repo.GH-1-slug"

	f := testkit.NewFakes(t)
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.RefsOrigin, "refs/remotes/origin/GH-1/slug\n")
	f.Script("git branch --track GH-1/slug origin/GH-1/slug", "")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1/slug", wt))
	f.Script("git worktree add "+wt+" GH-1/slug", "")
	f.Script("herdr worktree open --cwd /code/repo --path "+wt+" --label GH-1/slug", testkit.Opened)
	f.Script("herdr agent start "+agentctl.AgentName("GH-1", "", wt)+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+agentctl.AgentName(
			"GH-1",
			"",
			wt,
		)+" "+launch.Prompt(
			startcmd.ModeDefault,
			runplan.ShipFlow{},
			"GH-1",
			nil,
			"",
			nil,
			"",
		),
		"{}",
	)

	if err := start.Start(
		t.Context(),
		&bytes.Buffer{},
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1},
	); err != nil {
		t.Fatal(err)
	}

	if !f.Called("git branch --track GH-1/slug origin/GH-1/slug") || f.Called("git worktree add -b") {
		t.Errorf("origin's branch was not tracked: %v", f.Calls)
	}
}
