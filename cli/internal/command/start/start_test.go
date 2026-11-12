package start_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestPrompt(t *testing.T) {
	local, direct := runplan.ShipFlow{Name: "local"}, runplan.ShipFlow{Name: "direct", Planless: true}

	for _, c := range []struct {
		mode string
		fl   runplan.ShipFlow
		want string
	}{
		{startcmd.ModeDefault, runplan.ShipFlow{}, "/f10:plan GH-1 && /f10:ship GH-1 --driven"},
		{launch.ModePlan, runplan.ShipFlow{}, "/f10:plan GH-1 --driven"},
		{startcmd.ModeDefault, local, "/f10:plan GH-1 && /f10:ship local: GH-1 --driven"},
		{startcmd.ModeDefault, direct, "/f10:ship direct: GH-1 --driven"}, // a planless flow never plans
	} {
		if got := launch.Prompt(c.mode, c.fl, "GH-1", nil, "", nil, ""); got != c.want {
			t.Errorf("prompt(%q, %+v) = %q, want %q", c.mode, c.fl, got, c.want)
		}
	}

	dep := &launch.Dependency{
		Task:    "GH-7",
		Branch:  "GH-7/base",
		Plan:    "/code/repo.GH-7-base/.f10/plans/GH-7.md",
		Planned: true,
	}
	want := "/f10:plan GH-1 && GH-1 depends on GH-7, whose plan is at " +
		"/code/repo.GH-7-base/.f10/plans/GH-7.md --driven"

	if got := launch.Prompt(launch.ModePlan, runplan.ShipFlow{}, "GH-1", dep, "", nil, ""); got != want {
		t.Errorf("prompt with a dependency = %q, want %q", got, want)
	}

	// a saved plan is shipped, except under --plan, which plans again
	plan := "/code/repo.GH-1/.f10/plans/GH-1.md"
	for _, c := range []struct {
		mode string
		fl   runplan.ShipFlow
		want string
	}{
		{startcmd.ModeDefault, runplan.ShipFlow{}, "/f10:ship GH-1 --driven"},
		{launch.ModePlan, runplan.ShipFlow{}, "/f10:plan GH-1 --driven"},
		{startcmd.ModeDefault, local, "/f10:ship local: GH-1 --driven"},
	} {
		if got := launch.Prompt(c.mode, c.fl, "GH-1", nil, plan, nil, ""); got != c.want {
			t.Errorf("prompt(%q, %+v) with a plan = %q, want %q", c.mode, c.fl, got, c.want)
		}
	}

	want = "/f10:ship GH-1 && GH-1 depends on GH-7, whose plan is at " +
		"/code/repo.GH-7-base/.f10/plans/GH-7.md --driven"

	if got := launch.Prompt(startcmd.ModeDefault, runplan.ShipFlow{}, "GH-1", dep, plan, nil, ""); got != want {
		t.Errorf("prompt with a plan and a dependency = %q, want %q", got, want)
	}
}

func TestPromptNote(t *testing.T) {
	got := launch.Prompt(launch.ModeDiscuss, runplan.ShipFlow{}, "GH-1", nil, "", nil, "/f10:judge it first.")
	want := "Run `f10 task read GH-1` and summarise what it asks in a few lines. " +
		"Then, as the user asks: /f10:judge it first. Then stop and wait."

	if got != want {
		t.Errorf("prompt(discuss) with a note = %q, want %q", got, want)
	}

	pick := &hostops.Pickup{
		URL:  "https://github.com/o/r/pull/9",
		View: "f10 pr view --comments 9",
		Diff: "f10 pr diff 9",
	}
	if got := launch.Prompt(
		startcmd.ModeDefault,
		runplan.ShipFlow{},
		"GH-1",
		nil,
		"",
		pick,
		"check the tests",
	); !strings.HasSuffix(
		got,
		" Then, as the user asks: check the tests. Then stop and wait.",
	) {
		t.Errorf("prompt for a PR with a note = %q, want the note before the wait", got)
	}
}

func TestRefuseNote(t *testing.T) {
	pick := &hostops.Pickup{URL: "https://github.com/o/r/pull/9"}

	for _, tc := range []struct {
		mode   string
		pick   *hostops.Pickup
		note   string
		refuse bool
	}{
		{startcmd.ModeDefault, nil, "", false},
		{launch.ModeDiscuss, nil, "judge it first", false},
		{startcmd.ModeDefault, pick, "judge it first", false},
		{startcmd.ModeDefault, nil, "judge it first", true},
		{launch.ModePlan, nil, "judge it first", true},
		{launch.ModePlan, pick, "judge it first", true},
	} {
		if err := startcmd.RefuseNote(tc.mode, tc.pick, tc.note); (err != nil) != tc.refuse {
			t.Errorf(
				"refuseNote(%q, pick %v, %q) = %v, want refused %v",
				tc.mode,
				tc.pick != nil,
				tc.note,
				err,
				tc.refuse,
			)
		}
	}
}

// The whole flow with worktrunk present: wt creates, git is asked for the
// path, herdr opens, starts and prompts, and the report carries the facts.
func TestStartWithWorktrunk(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, "")
	f.Script(
		testkit.Worktrees,
		testkit.Porcelain(),
		testkit.Porcelain("GH-1", "/code/repo.GH-1"),
	) // git knows the worktree once wt ran
	f.Script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
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

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"task:", "GH-1", "branch:", "path:", "/code/repo.GH-1", "workspace:", "ws:3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}

	if f.Called("git worktree add") {
		t.Error("git created the worktree although wt is installed")
	}

	if f.Called("set branch.GH-1." + string(branchmeta.After)) {
		t.Error("a dependency was recorded although --after was not given")
	}

	if strings.Contains(strings.Join(f.Calls, "\n"), "--wait") {
		t.Error("the prompt waited on the agent")
	}
}

// A fresh start moves the task to its started status; a failure is a note
// beneath the report, never a refusal.
func TestStartMarksStarted(t *testing.T) {
	for name, fail := range map[string]bool{"moved": false, "failed": true} {
		f := testkit.NewFakes(t)
		f.Has["wt"] = true
		f.Script(testkit.RefsLocal, "")
		f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
		f.Script("wt switch --no-cd --yes --format json --create GH-1", "{}")
		f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
		f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")
		f.Script(
			"herdr agent prompt "+testkit.AgentOf(
				"GH-1",
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

		var moved []string

		started := func(_ context.Context, task ref.Ref) error {
			moved = append(moved, task.ID)
			if fail {
				return errors.New("notion: 503")
			}

			return nil
		}

		var out bytes.Buffer

		if err := start.Start(
			t.Context(),
			&out,
			start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, Started: started},
		); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if len(moved) != 1 || moved[0] != "GH-1" {
			t.Errorf("%s: moved %v, want GH-1", name, moved)
		}

		if got := strings.Contains(out.String(), "not moved to its started status: notion: 503"); got != fail {
			t.Errorf("%s: note shown = %v:\n%s", name, got, out.String())
		}
	}
}

// Without worktrunk, git creates the worktree at the sibling path; --after
// swaps the base and records the dependency, and a base with no plan yet is
// named with the path its plan will take, the report saying it is missing.
func TestStartWithGitAlone(t *testing.T) {
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
	local := runplan.ShipFlow{Name: "local"}
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
		)+" "+launch.Prompt(
			startcmd.ModeDefault,
			local,
			"GH-1",
			dep,
			"",
			nil,
			"",
		),
		"{}",
	)

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, Flow: local, After: "GH-7"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if f.Called("wt ") {
		t.Error("wt was run although it is not installed")
	}

	if !f.Called("set branch.GH-1." + string(branchmeta.Flow) + " local") {
		t.Error("the flow was not recorded on the branch")
	}

	if !f.Called(testkit.CfgTask) || !f.Called(testkit.CfgBranch) ||
		!f.Called("set branch.GH-1."+string(branchmeta.Tip)+" ccc") {
		t.Errorf("the dependency was not recorded: %v", f.Calls)
	}

	// the branch exists before anything is recorded about it
	created := testkit.IndexAfter(f.Calls, "git worktree add -b GH-1 /code/repo.GH-1 GH-7/base", -1)
	if created < 0 || testkit.IndexAfter(f.Calls, testkit.CfgTask, created) < 0 ||
		testkit.IndexAfter(f.Calls, testkit.CfgBranch, created) < 0 {
		t.Errorf("the dependency was recorded before the worktree existed: %v", f.Calls)
	}

	for _, want := range []string{"after:", "GH-7", "GH-7 has no plan yet at " + testkit.Storage.Plan("GH-7")} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), "same commit") {
		t.Errorf("a base that differs from the default was noted as equal:\n%s", out.String())
	}
}

// afterFixture scripts a worktrunk run of `start --after GH-7` where GH-7's
// branch is checked out at dir, and returns the fakes to add the prompt to.
func afterFixture(t *testing.T, dir, shaBase string) *testkit.Fakes {
	t.Helper()

	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.RefsAfter, testkit.Heads("GH-7/base"))
	f.Script(testkit.ShaAfter, shaBase)
	f.Script(testkit.HeadRef, "origin/main")
	f.Script(testkit.ShaMain, "bbb")
	f.Script(
		testkit.Worktrees,
		testkit.Porcelain("GH-7/base", dir),
		testkit.Porcelain("GH-7/base", dir, "GH-1", "/code/repo.GH-1"),
	)
	f.Script("wt switch --no-cd --yes --format json --create --base GH-7/base GH-1", "{}")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")

	return f
}

// A base task's plan found only in its own worktree, from before storage
// moved to main's, is the contract the prompt cites.
func TestStartAfterCitesThePlan(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, ".f10", "plans", "GH-7.md")

	if err := os.MkdirAll(filepath.Dir(plan), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(plan, []byte("# GH-7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := afterFixture(t, dir, "ccc")
	root := sharedStorage(f, storage.InRepo(dir))
	dep := &launch.Dependency{Task: "GH-7", Branch: "GH-7/base", Plan: plan, Planned: true}
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
		)+" "+launch.Prompt(
			startcmd.ModeDefault,
			runplan.ShipFlow{},
			"GH-1",
			dep,
			"",
			nil,
			"",
		),
		"{}",
	)

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: root, Task: testkit.Gh1, After: "GH-7"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " ") {
		t.Errorf("the agent was not prompted: %v", f.Calls)
	}

	if strings.Contains(out.String(), "no plan yet") || strings.Contains(out.String(), "out of reach") {
		t.Errorf("a plan that exists was reported missing:\n%s", out.String())
	}
}

// sharedStorage scripts the agent start a shared storage root adds its dirs
// to, and returns the root.
func sharedStorage(f *testkit.Fakes, root storage.Root) storage.Root {
	addDir := " -- " + strings.Join(start.AgentArgs(agentctl.Profile{}, root, "/code/repo.GH-1"), " ")
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7"+addDir, "{}")

	return root
}

// A base task whose plan is not written yet is named with the path the plan
// is expected at, and the report says it is missing.
func TestStartAfterWithoutAPlanYet(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, ".f10", "plans", "GH-7.md")

	f := afterFixture(t, dir, "ccc")
	root := sharedStorage(f, storage.InRepo(dir))
	dep := &launch.Dependency{Task: "GH-7", Branch: "GH-7/base", Plan: plan}
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
		)+" "+launch.Prompt(
			startcmd.ModeDefault,
			runplan.ShipFlow{},
			"GH-1",
			dep,
			"",
			nil,
			"",
		),
		"{}",
	)

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: root, Task: testkit.Gh1, After: "GH-7"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if want := "GH-7 has no plan yet at " + plan; !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// Out-of-tree storage shares one plans dir, so the plan is reachable
// whether or not the base task has a checkout.
func TestStartAfterOutOfTreePlan(t *testing.T) {
	root := storage.Root(t.TempDir())
	plan := root.Plan("GH-7")

	if err := os.MkdirAll(root.Plans(), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(plan, []byte("# GH-7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := afterFixture(t, "/code/repo.GH-7-base", "ccc")
	sharedStorage(f, root)
	dep := &launch.Dependency{Task: "GH-7", Branch: "GH-7/base", Plan: plan, Planned: true}
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
		)+" "+launch.Prompt(
			startcmd.ModeDefault,
			runplan.ShipFlow{},
			"GH-1",
			dep,
			"",
			nil,
			"",
		),
		"{}",
	)

	err := start.Start(
		t.Context(),
		&bytes.Buffer{},
		start.StartInput{Main: "/code/repo", Storage: root, Task: testkit.Gh1, After: "GH-7"},
	)
	if err != nil {
		t.Fatal(err)
	}
}

// A base task whose branch still sits at the default branch's commit has no
// code yet, and the note says so beneath the report.
func TestStartAfterEqualsDefault(t *testing.T) {
	f := afterFixture(t, "/code/repo.GH-7-base", "bbb")
	root := sharedStorage(f, storage.Root(t.TempDir()))
	dep := &launch.Dependency{Task: "GH-7", Branch: "GH-7/base", Plan: root.Plan("GH-7")}
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
		)+" "+launch.Prompt(
			startcmd.ModeDefault,
			runplan.ShipFlow{},
			"GH-1",
			dep,
			"",
			nil,
			"",
		),
		"{}",
	)

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: root, Task: testkit.Gh1, After: "GH-7"},
	)
	if err != nil {
		t.Fatal(err)
	}

	want := "--after GH-7: branch GH-7/base is at the same commit as the default branch origin/main, so GH-7 has no code yet"
	if !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// --base hands git the ref it found, and a base that is not at the
// default branch's commit gets no note.
func TestStartWithBase(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(testkit.ShaLocal, "aaa")
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.HeadRef, "origin/main")
	f.Script(testkit.ShaMain, "bbb")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("git worktree add -b GH-1 /code/repo.GH-1 release/1.2", "")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
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

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, Base: "release/1.2"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !f.Called("git worktree add -b GH-1 /code/repo.GH-1 release/1.2") {
		t.Error("the worktree was not created from the base")
	}

	if strings.Contains(out.String(), "same commit") {
		t.Errorf("a base that differs from the default was noted as equal:\n%s", out.String())
	}
}

// A base at the default branch's commit still creates the worktree from
// it, and says beneath the report that it changed nothing.
func TestStartBaseEqualsDefault(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script("git rev-parse --verify --quiet refs/heads/main", "aaa")
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.HeadRef, "origin/main")
	f.Script(testkit.ShaMain, "aaa")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("wt switch --no-cd --yes --format json --create --base main GH-1", "{}")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
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

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, Base: "main"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !f.Called("wt switch --no-cd --yes --format json --create --base main GH-1") {
		t.Error("the worktree was not created from the base")
	}

	want := "--base main is at the same commit as the default branch origin/main"
	if !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// A task that already has a branch keeps its base: --base is noted as
// ignored and never reaches wt.
func TestStartBaseIgnoredOnExistingBranch(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.ShaLocal, "aaa")
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("wt switch --no-cd --yes --format json GH-1", "{}")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
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

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, Base: "release/1.2"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "--base ignored") {
		t.Errorf("report missing the ignored note:\n%s", out.String())
	}

	if strings.Contains(strings.Join(f.Calls, "\n"), "--base") {
		t.Error("the base reached wt although the branch exists")
	}
}

// A workspace left open by a start that failed on a busy pane has no agent:
// its idle root shell gets one, and the agent gets its prompt.
func TestStartRecoversAWorkspaceWithNoAgent(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Reopened)
	f.Fail("herdr agent get pane:7", shell.Result{
		Code:   1,
		Stderr: `{"error":{"code":"agent_not_found","message":"agent target gh-1 not found"}}`,
	})
	f.Script("herdr pane process-info --pane pane:7",
		`{"result":{"process_info":{"foreground_process_group_id":42,"pane_id":"pane:7","shell_pid":42}}}`)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
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

	var out bytes.Buffer

	if err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1},
	); err != nil {
		t.Fatal(err)
	}

	if !f.Called("herdr agent prompt " + testkit.AgentOf("GH-1")) {
		t.Error("the recovered agent was not prompted")
	}

	if want := "started one in its idle shell"; !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// A fresh pane whose shell is still starting answers busy; start waits it
// out and prompts the agent once it starts.
func TestStartWaitsForTheShell(t *testing.T) {
	seam.Swap(t, &herdr.StartPoll, time.Millisecond)

	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Answers["herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7"] = []shell.Result{
		{
			Code:   1,
			Stderr: `{"error":{"code":"agent_pane_busy","message":"agent target pane pane:7 is not an available shell"}}`,
		},
		{Stdout: "{}"},
	}
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
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

	var progress bytes.Buffer

	if err := start.Start(
		t.Context(),
		io.Discard,
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, Progress: &progress},
	); err != nil {
		t.Fatal(err)
	}

	if !f.Called("herdr agent prompt " + testkit.AgentOf("GH-1")) {
		t.Error("the agent was not prompted once its pane was free")
	}

	if want := "waiting for the shell in pane pane:7 to finish starting"; !strings.Contains(progress.String(), want) {
		t.Errorf("progress = %q, want %q", progress.String(), want)
	}
}

// The default branch carries the title: the worktree lands at the
// flattened sibling path, the workspace is labelled with the full name,
// and the agent keeps the task's name.
func TestStartSlugsTheBranch(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, "")
	f.Script(
		testkit.Worktrees,
		testkit.Porcelain(),
		testkit.Porcelain("GH-1/add-dark-mode", "/code/repo.GH-1-add-dark-mode"),
	)
	f.Script("wt switch --no-cd --yes --format json --create GH-1/add-dark-mode", "{}")
	f.Script(
		"herdr worktree open --cwd /code/repo --path /code/repo.GH-1-add-dark-mode --label GH-1/add-dark-mode",
		testkit.Opened,
	)
	f.Script(
		"herdr agent start "+agentctl.AgentName(
			"GH-1",
			"",
			"/code/repo.GH-1-add-dark-mode",
		)+" --kind claude --pane pane:7",
		"{}",
	)
	f.Script(
		"herdr agent prompt "+agentctl.AgentName(
			"GH-1",
			"",
			"/code/repo.GH-1-add-dark-mode",
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

	var out bytes.Buffer

	err := start.Start(
		t.Context(),
		&out,
		start.StartInput{
			Main:    "/code/repo",
			Storage: testkit.Storage,
			Task:    testkit.Gh1,
			Title:   testkit.Titled("Add dark mode", nil),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"branch:", "GH-1/add-dark-mode", "/code/repo.GH-1-add-dark-mode"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), "bare id") {
		t.Errorf("a slugged branch was reported as the bare id:\n%s", out.String())
	}
}

// No title from the host still starts the task, on the bare id, and the
// report says why beneath the block.
func TestStartWithoutATitle(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
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

	var out bytes.Buffer

	in := start.StartInput{
		Main:    "/code/repo",
		Storage: testkit.Storage,
		Task:    testkit.Gh1,
		Title:   testkit.Titled("", errors.New("gh: offline")),
	}
	if err := start.Start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "no title for GH-1 (gh: offline), so the branch is the bare id") {
		t.Errorf("report missing the bare-id note:\n%s", out.String())
	}
}

// A worktree that exists but is not open in Herdr gets a fresh agent.
func TestStartOpensAClosedWorktree(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+testkit.AgentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.Script(
		"herdr agent prompt "+testkit.AgentOf(
			"GH-1",
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

	if !f.Called("herdr agent prompt " + testkit.AgentOf("GH-1")) {
		t.Error("the agent was not prompted")
	}
}

// A plan main saved stays in main: the agent ships it in place, and is
// started with main's task-file dirs added, since they lie outside its
// worktree.
func TestStartReadsMainsPlanInPlace(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	root := storage.InRepo(main)
	src := root.Plan("GH-1")
	testkit.WritePlan(t, src)

	agent := agentctl.AgentName("GH-1", "", wt)

	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", wt))
	f.Script("herdr worktree open --cwd "+main+" --path "+wt+" --label GH-1", testkit.Opened)
	f.Script("herdr agent start "+agent+" --kind claude --pane pane:7 -- "+
		strings.Join(start.AgentArgs(agentctl.Profile{}, root, wt), " "), "{}")
	f.Script(
		"herdr agent prompt "+agent+" "+launch.Prompt(
			startcmd.ModeDefault,
			runplan.ShipFlow{},
			"GH-1",
			nil,
			src,
			nil,
			"",
		),
		"{}",
	)

	var out bytes.Buffer

	if err := start.Start(
		t.Context(),
		&out,
		start.StartInput{Main: main, Task: testkit.Gh1, Storage: root},
	); err != nil {
		t.Fatal(err)
	}

	if !f.Called("herdr agent prompt " + agent + " /f10:ship GH-1 --driven") {
		t.Errorf("the agent was not prompted to ship: %v", f.Calls)
	}

	if _, err := os.Stat(src); err != nil {
		t.Errorf("main's plan moved: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wt, ".f10")); err == nil {
		t.Error("the worktree got a .f10 of its own")
	}

	if !strings.Contains(out.String(), src) {
		t.Errorf("report does not name the plan %s:\n%s", src, out.String())
	}
}

// An existing branch without a checkout gets one, without --create.
func TestStartChecksOutAnExistingBranch(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1/slug"))
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1/slug", "/code/repo.GH-1-slug"))
	f.Script("git worktree add /code/repo.GH-1-slug GH-1/slug", "")
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1-slug --label GH-1/slug", testkit.Opened)
	f.Script(
		"herdr agent start "+agentctl.AgentName("GH-1", "", "/code/repo.GH-1-slug")+" --kind claude --pane pane:7",
		"{}",
	)
	f.Script(
		"herdr agent prompt "+agentctl.AgentName("GH-1", "", "/code/repo.GH-1-slug")+" "+
			launch.Prompt(startcmd.ModeDefault, runplan.ShipFlow{}, "GH-1", nil, "", nil, ""),
		"{}",
	)

	if err := start.Start(
		t.Context(),
		&bytes.Buffer{},
		start.StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1},
	); err != nil {
		t.Fatal(err)
	}

	if !f.Called("git worktree add /code/repo.GH-1-slug GH-1/slug") {
		t.Error("the existing branch was not checked out")
	}
}

// Every mode but --discuss refuses a parent; a PR pickup never does.
func TestChecksParent(t *testing.T) {
	for _, mode := range []string{startcmd.ModeDefault, launch.ModePlan} {
		if !start.ChecksParent(mode, nil) {
			t.Errorf("mode %q skips the parent check", mode)
		}
	}

	if start.ChecksParent(launch.ModeDiscuss, nil) {
		t.Error("--discuss refuses a parent")
	}

	if start.ChecksParent(startcmd.ModeDefault, testkit.PickGH) || start.ChecksParent(launch.ModePlan, testkit.PickGH) {
		t.Error("a PR pickup refuses a parent")
	}
}
