package start

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A --after that points to no branch refuses before git, wt or herdr
// create or record anything.
func TestStartRefusesMissingAfter(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(testkit.RefsAfter, "")
	f.Script(testkit.RefsAfterO, "")

	err := Start(
		t.Context(),
		&bytes.Buffer{},
		StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, After: "GH-7"},
	)
	if err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Fatalf("err = %v, want the missing base refused", err)
	}

	if f.Called("wt ") || f.Called("git worktree add") || f.Called("set branch.") || f.Called("herdr") {
		t.Errorf("something was created after the refusal: %v", f.Calls)
	}
}

// A --base that points nowhere refuses before git, wt or herdr create anything.
func TestStartRefusesMissingBase(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Fail(testkit.ShaLocal, testkit.Missing)
	f.Fail(testkit.ShaRemote, testkit.Missing)
	f.Fail(testkit.ShaOrigin, testkit.Missing)

	err := Start(
		t.Context(),
		&bytes.Buffer{},
		StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1, Base: "release/1.2"},
	)
	if err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Fatalf("err = %v, want the missing base refused", err)
	}

	if f.Called("wt ") || f.Called("git worktree add") || f.Called("herdr") {
		t.Errorf("something was created after the refusal: %v", f.Calls)
	}
}

// A second run for the same task opens the worktree that exists: Herdr
// already shows it, so no agent is started or prompted. --after is noted as
// ignored for the base, which the branch keeps, and the dependency recorded.
func TestStartReusesTheWorktreeAndWorkspace(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.RefsAfter, testkit.Heads("GH-7"))
	f.Script("git rev-parse --verify --quiet GH-7", "ccc")

	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Reopened)
	f.Script("herdr agent get pane:7", testkit.AgentFound)

	var out bytes.Buffer

	err := Start(
		t.Context(),
		&out,
		StartInput{
			Main:    "/code/repo",
			Storage: testkit.Storage,
			Task:    testkit.Gh1,
			Mode:    launch.ModePlan,
			After:   "GH-7",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if f.Called("wt ") || f.Called("git worktree add") {
		t.Error("a worktree was created for a branch that has one")
	}

	if f.Called("herdr agent start") || f.Called("herdr agent prompt") {
		t.Error("an agent was started in a workspace that already had one")
	}

	if !f.Called("set branch.GH-1.f10-after-branch GH-7") {
		t.Error("the dependency was not recorded on the existing branch")
	}

	for _, want := range []string{
		"reused the existing worktree",
		"--after ignored for the base",
		"dependency on GH-7 recorded",
		"workspace already open",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// A second start on a checkout Herdr already shows adopts that workspace
// instead of asking Herdr to open it, which can answer with a duplicate.
func TestStartAdoptsTheWorkspaceHerdrShows(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("herdr workspace list", `{"result":{"workspaces":[`+
		`{"workspace_id":"ws:3","label":"GH-1","worktree":{"checkout_path":"/code/repo.GH-1"}}]}}`)
	f.Script("herdr pane list --workspace ws:3", `{"result":{"panes":[{"pane_id":"pane:7","agent":"claude"}]}}`)
	f.Script("herdr agent get pane:7", testkit.AgentFound)

	var out bytes.Buffer

	if err := Start(
		t.Context(),
		&out,
		StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1},
	); err != nil {
		t.Fatal(err)
	}

	if f.Called("herdr worktree open") || f.Called("herdr agent start") || f.Called("herdr agent prompt") {
		t.Errorf("calls = %q", f.Calls)
	}

	for _, want := range []string{"ws:3", "workspace already open"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// A workspace with no agent whose root pane runs something else is the
// user's: nothing is started there and nothing is prompted.
func TestStartLeavesABusyPaneAlone(t *testing.T) {
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
		`{"result":{"process_info":{"foreground_process_group_id":77,"pane_id":"pane:7","shell_pid":42}}}`)

	var out bytes.Buffer

	if err := Start(
		t.Context(),
		&out,
		StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1},
	); err != nil {
		t.Fatal(err)
	}

	if f.Called("herdr agent start") || f.Called("herdr agent prompt") {
		t.Error("an agent was started over what the pane runs")
	}

	if want := "runs something other than an idle shell"; !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// A task with a bare-id branch keeps it over the slug: git cannot hold GH-1
// and GH-1/<slug> at once, and the existing one is the work. No bare-id
// note either, since this run named nothing.
func TestStartReusesABareBranchOverTheSlug(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Has["wt"] = true
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	f.Script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", testkit.Reopened)
	f.Script("herdr agent get pane:7", testkit.AgentFound)

	var out bytes.Buffer

	err := Start(
		t.Context(),
		&out,
		StartInput{
			Main:    "/code/repo",
			Storage: testkit.Storage,
			Task:    testkit.Gh1,
			Title:   testkit.Titled("", errors.New("gh: offline")),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if f.Called("wt ") {
		t.Error("a worktree was created although the task's branch has one")
	}

	if strings.Contains(out.String(), "bare id") {
		t.Errorf("the note explains a name that was not given:\n%s", out.String())
	}
}

// A parent with unfinished subtasks is refused with ui.ExitParent, reading no
// more than whether the task has a branch; a started task, one whose subtasks
// are all finished and one with none start.
func TestStartRefusesAParent(t *testing.T) {
	kidsOf := func(rows []stack.ChildRow, err error, asked *bool) func(context.Context, ref.Ref) ([]stack.ChildRow, error) {
		return func(context.Context, ref.Ref) ([]stack.ChildRow, error) {
			*asked = true

			return rows, err
		}
	}

	t.Run("open subtasks", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(testkit.RefsLocal, "")

		var asked bool

		rows := []stack.ChildRow{{ID: "GH-2"}, {ID: "GH-3", Finished: true}, {ID: "GH-4"}}
		in := StartInput{
			Main:     "/code/repo",
			Storage:  testkit.Storage,
			Task:     testkit.Gh1,
			Children: kidsOf(rows, nil, &asked),
		}
		err := RefuseStartParent(t.Context(), in)

		var coder cli.ExitCoder
		if !errors.As(err, &coder) || coder.ExitCode() != ui.ExitParent {
			t.Fatalf("err = %v, want exit %d", err, ui.ExitParent)
		}

		for _, want := range []string{"GH-1 is a parent of GH-2, GH-4", "/f10:drive GH-1", "f10 start GH-2"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message missing %q: %v", want, err)
			}
		}

		if !asked {
			t.Error("the refusal did not come from the subtask lookup")
		}

		for _, c := range f.Calls {
			if !strings.HasPrefix(c, "git for-each-ref") {
				t.Errorf("%q ran for the check, which only looks for the task's branch", c)
			}
		}
	})

	for name, tc := range map[string]struct {
		rows []stack.ChildRow
		err  error
	}{
		"every subtask finished": {rows: []stack.ChildRow{{ID: "GH-2", Finished: true}}},
		"no subtasks":            {rows: []stack.ChildRow{}},
		"no parent link":         {err: hostops.ErrNoParentLink},
	} {
		t.Run(name, func(t *testing.T) {
			f := testkit.NewFakes(t)
			f.Script(testkit.RefsLocal, "")

			var asked bool

			in := StartInput{
				Main:     "/code/repo",
				Storage:  testkit.Storage,
				Task:     testkit.Gh1,
				Children: kidsOf(tc.rows, tc.err, &asked),
			}
			if err := RefuseStartParent(t.Context(), in); err != nil || !asked {
				t.Errorf("err = %v, asked = %v; want a pass after asking", err, asked)
			}
		})
	}

	t.Run("a started task", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))

		var asked bool

		in := StartInput{
			Main:     "/code/repo",
			Storage:  testkit.Storage,
			Task:     testkit.Gh1,
			Children: kidsOf([]stack.ChildRow{{ID: "GH-2"}}, nil, &asked),
		}
		if err := RefuseStartParent(t.Context(), in); err != nil || asked {
			t.Errorf("err = %v, asked = %v; a started task is never a parent", err, asked)
		}
	})

	t.Run("no check", func(t *testing.T) {
		testkit.NewFakes(t)

		if err := RefuseStartParent(
			t.Context(),
			StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Gh1},
		); err != nil {
			t.Errorf("err = %v with no children lookup", err)
		}
	})
}
