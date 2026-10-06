package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// A fakes stands in for git, wt, herdr and the driver: every call is
// recorded, every answer is scripted, and nothing is executed. The key is
// the program name and its arguments joined by spaces. A key scripted with
// several answers hands them out in order and repeats the last, which is
// how a listing changes once something was created.
type fakes struct {
	t       *testing.T
	calls   []string
	answers map[string][]shell.Result
	has     map[string]bool
}

func newFakes(t *testing.T) *fakes {
	t.Helper()

	f := &fakes{t: t, answers: map[string][]shell.Result{}, has: map[string]bool{"herdr": true}}

	prevExec, prevCapture, prevHas := gitx.Exec, shell.Capture, shell.Has

	gitx.Exec = func(_ context.Context, _ string, args ...string) (string, error) {
		res := f.answer("git " + strings.Join(args, " "))
		if res.Code != 0 {
			return "", errors.New(res.Stderr)
		}

		return strings.TrimSpace(res.Stdout), nil
	}

	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		return f.answer(filepath.Base(name) + " " + strings.Join(args, " ")), nil
	}

	shell.Has = func(name string) bool { return f.has[name] }

	t.Cleanup(func() { gitx.Exec, shell.Capture, shell.Has = prevExec, prevCapture, prevHas })
	t.Setenv("HERDR_ENV", "1")

	return f
}

func (f *fakes) answer(key string) shell.Result {
	f.calls = append(f.calls, key)

	queue, ok := f.answers[key]
	if !ok {
		f.t.Fatalf("unscripted call: %q\nscripted:\n  %s", key, strings.Join(f.keys(), "\n  "))
	}

	if len(queue) > 1 {
		f.answers[key] = queue[1:]
	}

	return queue[0]
}

func (f *fakes) keys() []string {
	var ks []string
	for k := range f.answers {
		ks = append(ks, k)
	}

	return ks
}

// script sets the answers for key: one per call, the last repeating.
func (f *fakes) script(key string, stdouts ...string) {
	f.answers[key] = nil
	for _, s := range stdouts {
		f.answers[key] = append(f.answers[key], shell.Result{Stdout: s})
	}
}

func (f *fakes) fail(key string, res shell.Result) { f.answers[key] = []shell.Result{res} }

func (f *fakes) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}

	return false
}

// fakeDriver is an executable at <root>/driver so driver.Find accepts it;
// what it answers comes from the fakes, never from running it.
func fakeDriver(t *testing.T) *driver.Driver {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, "driver")

	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return driver.Find(root, root)
}

const (
	refsLocal  = "git for-each-ref --format=%(refname) refs/heads/GH-1 refs/heads/GH-1-*"
	refsAfter  = "git for-each-ref --format=%(refname) refs/heads/GH-7 refs/heads/GH-7-*"
	refsAfterO = "git for-each-ref --format=%(refname) refs/remotes/origin/GH-7 refs/remotes/origin/GH-7-*"
	shaAfter   = "git rev-parse --verify --quiet GH-7/base"
	cfgTask    = "git config branch.GH-1.f10-after GH-7"
	cfgBranch  = "git config branch.GH-1.f10-after-branch GH-7/base"
	worktrees  = "git worktree list --porcelain"
	shaLocal   = "git rev-parse --verify --quiet refs/heads/release/1.2"
	shaRemote  = "git rev-parse --verify --quiet refs/remotes/release/1.2"
	shaOrigin  = "git rev-parse --verify --quiet refs/remotes/origin/release/1.2"
	headRef    = "git symbolic-ref --short refs/remotes/origin/HEAD"
	shaMain    = "git rev-parse --verify --quiet refs/remotes/origin/main"
	opened     = `{"result":{"workspace":{"workspace_id":"ws:3"},"root_pane":{"pane_id":"pane:7"},"already_open":false}}`
	reopened   = `{"result":{"workspace":{"workspace_id":"ws:3"},"root_pane":{"pane_id":"pane:7"},"already_open":true}}`
)

var gh1 = ref.Ref{ID: "GH-1", Number: "1", Origin: ref.OriginExplicit}

// missing is what rev-parse --verify --quiet answers for a ref that is not there.
var missing = shell.Result{Code: 1}

// heads prefixes branch names the way for-each-ref prints them.
func heads(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("refs/heads/" + n + "\n")
	}

	return b.String()
}

// porcelain is a worktree listing of main plus the given branch/path pairs.
func porcelain(pairs ...string) string {
	var b strings.Builder

	b.WriteString("worktree /code/repo\nHEAD a\nbranch refs/heads/main\n")

	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString("\nworktree " + pairs[i+1] + "\nHEAD b\nbranch refs/heads/" + pairs[i] + "\n")
	}

	return b.String()
}

// titled is a title lookup that answers with one title, or fails.
func titled(title string, err error) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return title, err }
}

func TestBranchName(t *testing.T) {
	cases := []struct {
		name     string
		driver   bool
		answer   shell.Result
		title    func(context.Context) (string, error)
		suffix   string
		want     string
		wantNote string
	}{
		{name: "no driver slugs the title", title: titled("Add dark mode", nil), want: "GH-1/add-dark-mode"},
		{
			name:   "driver names the branch",
			driver: true,
			answer: shell.Result{Stdout: "GH-1/short-slug\n"},
			title:  titled("unused", nil),
			want:   "GH-1/short-slug",
		},
		{
			name:   "driver declining slugs the title",
			driver: true,
			answer: shell.Result{Code: 3},
			title:  titled("Add dark mode", nil),
			want:   "GH-1/add-dark-mode",
		},
		{
			name:   "suffix follows the driver's name",
			driver: true,
			answer: shell.Result{Stdout: "GH-1/slug"},
			suffix: "v2",
			want:   "GH-1/slug-v2",
		},
		{
			name:   "suffix follows the slug",
			title:  titled("Add dark mode", nil),
			suffix: "v2",
			want:   "GH-1/add-dark-mode-v2",
		},
		{
			name:     "no title is the bare id, with the reason",
			title:    titled("", errors.New("gh: offline")),
			want:     "GH-1",
			wantNote: "no title for GH-1 (gh: offline), so the branch is the bare id",
		},
		{
			name:     "a title with no slug in it is the bare id",
			title:    titled("Исправить", nil),
			want:     "GH-1",
			wantNote: "the title of GH-1 yields no slug, so the branch is the bare id",
		},
		{
			name:     "no title source is the bare id",
			want:     "GH-1",
			wantNote: "no title source for GH-1, so the branch is the bare id",
		},
		{
			name:     "suffix follows the bare id",
			suffix:   "attempt2",
			want:     "GH-1-attempt2",
			wantNote: "no title source for GH-1, so the branch is the bare id",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakes(t)

			var d *driver.Driver
			if c.driver {
				d = fakeDriver(t)
				f.fail("driver branch 1", c.answer)
			}

			got, note, err := branchName(t.Context(), d, gh1, c.suffix, c.title)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("branchName = %q, want %q", got, c.want)
			}

			if note != c.wantNote {
				t.Errorf("note = %q, want %q", note, c.wantNote)
			}
		})
	}
}

func TestBranchNameDriverFailure(t *testing.T) {
	f := newFakes(t)
	f.fail("driver branch 1", shell.Result{Code: 1, Stderr: "tracker down"})

	_, _, err := branchName(t.Context(), fakeDriver(t), gh1, "", titled("unused", nil))
	if err == nil || !strings.Contains(err.Error(), "tracker down") {
		t.Errorf("err = %v, want the driver's stderr", err)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ title, want string }{
		{title: "Add dark mode", want: "add-dark-mode"},
		{title: "  f10 start: --after <task-id> records a dependency!  ", want: "f10-start-after-task-id-records-a"},
		{title: "Fix (again) the CSV/XLSX export", want: "fix-again-the-csv-xlsx-export"},
		{
			title: "Default task ids from the project name, default branches as <ID>/<slug>",
			want:  "default-task-ids-from-the-project-name",
		},
		{title: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{title: "exactly-forty-characters-long-slug-here!", want: "exactly-forty-characters-long-slug-here"},
		{title: "Поддержка кириллицы", want: ""},
		{title: "---", want: ""},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			if got := slugify(c.title); got != c.want {
				t.Errorf("slugify(%q) = %q, want %q", c.title, got, c.want)
			}

			if len(c.want) > slugMax {
				t.Errorf("case expects %d characters, over the %d cut", len(c.want), slugMax)
			}
		})
	}
}

// The title comes from the host's own issue, through the one field gh is
// asked for and the whole issue glab answers with.
func TestHostIssueTitle(t *testing.T) {
	f := newFakes(t)
	f.script("gh issue view 1 --json title", `{"title":"Add dark mode"}`)
	f.script("glab issue view 1 -F json", `{"iid":1,"title":"Dunkelmodus","web_url":"https://x"}`)
	f.fail("gh issue view 2 --json title", shell.Result{Code: 1, Stderr: "no issue 2"})
	f.script("gh issue view 3 --json title", `{"title":""}`)

	for name, want := range map[string]string{"gh": "Add dark mode", "glab": "Dunkelmodus"} {
		got, err := host{name: name, dir: "/code/repo"}.issueTitle(t.Context(), "1")
		if err != nil || got != want {
			t.Errorf("%s title = %q, %v; want %q", name, got, err, want)
		}
	}

	gh := host{name: "gh", dir: "/code/repo"}

	if _, err := gh.issueTitle(t.Context(), "2"); err == nil || !strings.Contains(err.Error(), "no issue 2") {
		t.Errorf("err = %v, want gh's stderr", err)
	}

	if _, err := gh.issueTitle(t.Context(), "3"); err == nil || !strings.Contains(err.Error(), "no title") {
		t.Errorf("err = %v, want no title", err)
	}
}

func TestTaskBranch(t *testing.T) {
	t.Run("local branch wins", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, heads("GH-7/thing"))

		got, err := taskBranch(t.Context(), "/code/repo", "GH-7")
		if err != nil || got != "GH-7/thing" {
			t.Errorf("taskBranch = %q, %v", got, err)
		}

		if f.called("git for-each-ref --format=%(refname) refs/remotes") {
			t.Error("origin was searched although a local branch matched")
		}
	})

	t.Run("origin is the fallback", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, "")
		f.script(refsAfterO, "refs/remotes/origin/GH-7")

		got, err := taskBranch(t.Context(), "/code/repo", "GH-7")
		if err != nil || got != "origin/GH-7" {
			t.Errorf("taskBranch = %q, %v", got, err)
		}
	})

	t.Run("nothing anywhere fails", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, "")
		f.script(refsAfterO, "")

		if _, err := taskBranch(t.Context(), "/code/repo", "GH-7"); err == nil {
			t.Error("taskBranch found a branch where none exists")
		}
	})

	t.Run("several is an error naming them", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, heads("GH-7", "GH-7-attempt2"))

		_, err := taskBranch(t.Context(), "/code/repo", "GH-7")
		if err == nil || !strings.Contains(err.Error(), "GH-7-attempt2") {
			t.Errorf("err = %v, want the candidates named", err)
		}
	})
}

func TestNamedRef(t *testing.T) {
	t.Run("local branch wins", func(t *testing.T) {
		f := newFakes(t)
		f.script(shaLocal, "aaa")

		got, sha, err := namedRef(t.Context(), "/code/repo", "release/1.2")
		if err != nil || got != "release/1.2" || sha != "aaa" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}

		if f.called("git rev-parse --verify --quiet refs/remotes") {
			t.Error("remotes were searched although a local branch matched")
		}
	})

	t.Run("a remote typed as such is taken literally", func(t *testing.T) {
		f := newFakes(t)
		f.fail("git rev-parse --verify --quiet refs/heads/origin/main", missing)
		f.script("git rev-parse --verify --quiet refs/remotes/origin/main", "bbb")

		got, sha, err := namedRef(t.Context(), "/code/repo", "origin/main")
		if err != nil || got != "origin/main" || sha != "bbb" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}
	})

	t.Run("origin is the fallback", func(t *testing.T) {
		f := newFakes(t)
		f.fail(shaLocal, missing)
		f.fail(shaRemote, missing)
		f.script(shaOrigin, "ccc")

		got, sha, err := namedRef(t.Context(), "/code/repo", "release/1.2")
		if err != nil || got != "origin/release/1.2" || sha != "ccc" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}
	})

	t.Run("nothing anywhere fails naming the branch", func(t *testing.T) {
		f := newFakes(t)
		f.fail(shaLocal, missing)
		f.fail(shaRemote, missing)
		f.fail(shaOrigin, missing)

		_, _, err := namedRef(t.Context(), "/code/repo", "release/1.2")
		if err == nil || !strings.Contains(err.Error(), "release/1.2") {
			t.Errorf("err = %v, want the branch named", err)
		}
	})
}

func TestExistingBranch(t *testing.T) {
	cases := []struct {
		name    string
		refs    string
		want    string
		suffix  string
		got     string
		wantErr bool
	}{
		{name: "none", refs: "", want: "GH-1", got: ""},
		{name: "the intended name wins", refs: heads("GH-1-attempt2", "GH-1"), want: "GH-1", got: "GH-1"},
		{
			name: "one task branch is reused whatever its shape",
			refs: heads("GH-1/old-slug"),
			want: "GH-1/new-slug",
			got:  "GH-1/old-slug",
		},
		{
			name:   "a suffix narrows",
			refs:   heads("GH-1", "GH-1-attempt2", "GH-1/slug-attempt2"),
			want:   "GH-1-attempt2",
			suffix: "attempt2",
			got:    "GH-1-attempt2",
		},
		{
			name:   "a suffix with no match creates",
			refs:   heads("GH-1", "GH-1-attempt2"),
			want:   "GH-1-v3",
			suffix: "v3",
			got:    "",
		},
		{name: "several without the intended name", refs: heads("GH-1/a", "GH-1/b"), want: "GH-1", wantErr: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakes(t)
			f.script(refsLocal, c.refs)

			got, err := existingBranch(t.Context(), "/code/repo", c.want, "GH-1", c.suffix)
			if c.wantErr {
				if err == nil {
					t.Fatalf("existingBranch = %q, want an error", got)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got != c.got {
				t.Errorf("existingBranch = %q, want %q", got, c.got)
			}
		})
	}
}

func TestPrompt(t *testing.T) {
	cases := map[string]string{
		modeDefault: "/f10:plan GH-1 && /f10:ship GH-1 && take defaults for all gaps; we review them at the end --driven",
		modePlan:    "/f10:plan GH-1 --driven",
		modeLocal:   "/f10:plan GH-1 && /f10:ship local: GH-1 && take defaults for all gaps; we review them at the end --driven",
	}

	for mode, want := range cases {
		if got := prompt(mode, "GH-1", nil); got != want {
			t.Errorf("prompt(%q) = %q, want %q", mode, got, want)
		}
	}

	dep := &dependency{
		task:    "GH-7",
		branch:  "GH-7/base",
		plan:    "/code/repo.GH-7-base/.f10/plans/GH-7.md",
		planned: true,
	}
	want := "/f10:plan GH-1 && GH-1 depends on GH-7: design against GH-7's planned API as its plan at " +
		"/code/repo.GH-7-base/.f10/plans/GH-7.md records it, not against this checkout, and write no fallback for its absence" +
		" --driven"

	if got := prompt(modePlan, "GH-1", dep); got != want {
		t.Errorf("prompt with a dependency = %q, want %q", got, want)
	}
}

func TestSiblingPathAndAgentName(t *testing.T) {
	if got, want := siblingPath("/code/repo", "GH-1/slug"), "/code/repo.GH-1-slug"; got != want {
		t.Errorf("siblingPath = %q, want %q", got, want)
	}

	if got, want := agentName("GH-1", ""), "gh-1"; got != want {
		t.Errorf("agentName = %q, want %q", got, want)
	}

	if got, want := agentName("GH-1", "Attempt2"), "gh-1-attempt2"; got != want {
		t.Errorf("agentName = %q, want %q", got, want)
	}
}

func TestDeclaresPipeline(t *testing.T) {
	eff := &facts.Effective{Fields: []facts.Field{{
		Name:  "Ship pipeline(s)",
		Value: "- default: implement -> review (local) -> pr\n- Local: implement -> commit\n",
		Prose: true,
	}}}

	if !declaresPipeline(&target{eff: eff}, "local") {
		t.Error("a declared local pipeline was not found")
	}

	if declaresPipeline(&target{eff: eff}, "direct") {
		t.Error("an undeclared pipeline was found")
	}

	if declaresPipeline(&target{eff: &facts.Effective{}}, "local") {
		t.Error("a project with no pipelines declares local")
	}
}

// The whole flow with worktrunk present: wt creates, git is asked for the
// path, herdr opens, starts and prompts, and the report carries the facts.
func TestStartWithWorktrunk(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1")) // git knows the worktree once wt ran
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", nil), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"task:", "GH-1", "branch:", "path:", "/code/repo.GH-1", "workspace:", "ws:3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}

	if f.called("git worktree add") {
		t.Error("git created the worktree although wt is installed")
	}

	if f.called("git config") {
		t.Error("a dependency was recorded although --after was not given")
	}

	if strings.Contains(strings.Join(f.calls, "\n"), "--wait") {
		t.Error("the prompt waited on the agent")
	}
}

// Without worktrunk, git creates the worktree at the sibling path, and
// --after swaps the default base for the task's branch and records the
// dependency. The base task has no checkout here, so the prompt names it
// without a plan path and the report says why.
func TestStartWithGitAlone(t *testing.T) {
	f := newFakes(t)
	f.script(refsLocal, "")
	f.script(refsAfter, heads("GH-7/base"))
	f.script(shaAfter, "ccc")
	f.script(headRef, "origin/main")
	f.script(shaMain, "bbb")
	f.script(cfgTask, "")
	f.script(cfgBranch, "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("git worktree add -b GH-1 /code/repo.GH-1 GH-7/base", "")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")

	dep := &dependency{task: "GH-7", branch: "GH-7/base"}
	f.script("herdr agent prompt gh-1 "+prompt(modeLocal, "GH-1", dep), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, mode: modeLocal, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if f.called("wt ") {
		t.Error("wt was run although it is not installed")
	}

	if !f.called(cfgTask) || !f.called(cfgBranch) {
		t.Errorf("the dependency was not recorded: %v", f.calls)
	}

	// the branch exists before anything is recorded about it
	created := indexAfter(f.calls, "git worktree add -b GH-1 /code/repo.GH-1 GH-7/base", -1)
	if created < 0 || indexAfter(f.calls, cfgTask, created) < 0 || indexAfter(f.calls, cfgBranch, created) < 0 {
		t.Errorf("the dependency was recorded before the worktree existed: %v", f.calls)
	}

	for _, want := range []string{"after:", "GH-7", "GH-7 has no checkout here"} {
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
func afterFixture(t *testing.T, dir, shaBase string) *fakes {
	t.Helper()

	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(refsAfter, heads("GH-7/base"))
	f.script(shaAfter, shaBase)
	f.script(headRef, "origin/main")
	f.script(shaMain, "bbb")
	f.script(cfgTask, "")
	f.script(cfgBranch, "")
	f.script(worktrees, porcelain("GH-7/base", dir), porcelain("GH-7/base", dir, "GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create --base GH-7/base GH-1", "{}")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")

	return f
}

// With in-repo storage the base task's plan sits in its own worktree, and
// the prompt cites that path as the contract to design against.
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
	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: plan, planned: true}
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", dep), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if !f.called("herdr agent prompt gh-1 ") {
		t.Errorf("the agent was not prompted: %v", f.calls)
	}

	if strings.Contains(out.String(), "no plan yet") || strings.Contains(out.String(), "out of reach") {
		t.Errorf("a plan that exists was reported missing:\n%s", out.String())
	}
}

// A base task whose plan is not written yet is named with the path the plan
// is expected at, and the report says it is missing.
func TestStartAfterWithoutAPlanYet(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, ".f10", "plans", "GH-7.md")

	f := afterFixture(t, dir, "ccc")
	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: plan}
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", dep), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, after: "GH-7"})
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
	plans := t.TempDir()
	plan := filepath.Join(plans, "GH-7.md")

	if err := os.WriteFile(plan, []byte("# GH-7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := afterFixture(t, "/code/repo.GH-7-base", "ccc")
	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: plan, planned: true}
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", dep), "{}")

	err := start(
		t.Context(),
		&bytes.Buffer{},
		startInput{main: "/code/repo", task: gh1, after: "GH-7", plansDir: plans},
	)
	if err != nil {
		t.Fatal(err)
	}
}

// A base task whose branch still sits at the default branch's commit has no
// code yet, and the note says so beneath the report.
func TestStartAfterEqualsDefault(t *testing.T) {
	f := afterFixture(t, "/code/repo.GH-7-base", "bbb")
	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: "/code/repo.GH-7-base/.f10/plans/GH-7.md"}
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", dep), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	want := "--after GH-7: branch GH-7/base is at the same commit as the default branch origin/main, so GH-7 has no code yet"
	if !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// A --after that points to no branch refuses before git, wt or herdr
// create or record anything.
func TestStartRefusesMissingAfter(t *testing.T) {
	f := newFakes(t)
	f.script(refsAfter, "")
	f.script(refsAfterO, "")

	err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1, after: "GH-7"})
	if err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Fatalf("err = %v, want the missing base refused", err)
	}

	if f.called("wt ") || f.called("git worktree add") || f.called("git config") || f.called("herdr") {
		t.Errorf("something was created after the refusal: %v", f.calls)
	}
}

// --base hands git the ref it found, and a base that is not at the
// default branch's commit gets no note.
func TestStartWithBase(t *testing.T) {
	f := newFakes(t)
	f.script(shaLocal, "aaa")
	f.script(refsLocal, "")
	f.script(headRef, "origin/main")
	f.script(shaMain, "bbb")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("git worktree add -b GH-1 /code/repo.GH-1 release/1.2", "")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", nil), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, base: "release/1.2"})
	if err != nil {
		t.Fatal(err)
	}

	if !f.called("git worktree add -b GH-1 /code/repo.GH-1 release/1.2") {
		t.Error("the worktree was not created from the base")
	}

	if strings.Contains(out.String(), "same commit") {
		t.Errorf("a base that differs from the default was noted as equal:\n%s", out.String())
	}
}

// A base at the default branch's commit still creates the worktree from
// it, and says beneath the report that it changed nothing.
func TestStartBaseEqualsDefault(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script("git rev-parse --verify --quiet refs/heads/main", "aaa")
	f.script(refsLocal, "")
	f.script(headRef, "origin/main")
	f.script(shaMain, "aaa")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create --base main GH-1", "{}")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", nil), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, base: "main"})
	if err != nil {
		t.Fatal(err)
	}

	if !f.called("wt switch --no-cd --yes --format json --create --base main GH-1") {
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
	f := newFakes(t)
	f.has["wt"] = true
	f.script(shaLocal, "aaa")
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json GH-1", "{}")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", nil), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, base: "release/1.2"})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "--base ignored") {
		t.Errorf("report missing the ignored note:\n%s", out.String())
	}

	if strings.Contains(strings.Join(f.calls, "\n"), "--base") {
		t.Error("the base reached wt although the branch exists")
	}
}

// A --base that points nowhere refuses before git, wt or herdr create anything.
func TestStartRefusesMissingBase(t *testing.T) {
	f := newFakes(t)
	f.fail(shaLocal, missing)
	f.fail(shaRemote, missing)
	f.fail(shaOrigin, missing)

	err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1, base: "release/1.2"})
	if err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Fatalf("err = %v, want the missing base refused", err)
	}

	if f.called("wt ") || f.called("git worktree add") || f.called("herdr") {
		t.Errorf("something was created after the refusal: %v", f.calls)
	}
}

// A second run for the same task opens the worktree that exists and does
// not fail: Herdr already shows it, so its root pane is not at a prompt and
// no agent is started or prompted. --after is noted as ignored for the base
// since the branch keeps it, and the dependency is recorded all the same.
func TestStartReusesTheWorktreeAndWorkspace(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(refsAfter, heads("GH-7"))
	f.script("git rev-parse --verify --quiet GH-7", "ccc")
	f.script("git config branch.GH-1.f10-after GH-7", "")
	f.script("git config branch.GH-1.f10-after-branch GH-7", "")
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", reopened)

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, mode: modePlan, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if f.called("wt ") || f.called("git worktree add") {
		t.Error("a worktree was created for a branch that has one")
	}

	if f.called("herdr agent") {
		t.Error("an agent was started in a workspace that already had one")
	}

	if !f.called("git config branch.GH-1.f10-after-branch GH-7") {
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

// The default branch carries the title: the worktree lands at the
// flattened sibling path, the workspace is labelled with the full name,
// and the agent keeps the task's name.
func TestStartSlugsTheBranch(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1/add-dark-mode", "/code/repo.GH-1-add-dark-mode"))
	f.script("wt switch --no-cd --yes --format json --create GH-1/add-dark-mode", "{}")
	f.script("herdr worktree open --path /code/repo.GH-1-add-dark-mode --label GH-1/add-dark-mode", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", nil), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, title: titled("Add dark mode", nil)})
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
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", nil), "{}")

	var out bytes.Buffer

	in := startInput{main: "/code/repo", task: gh1, title: titled("", errors.New("gh: offline"))}
	if err := start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "no title for GH-1 (gh: offline), so the branch is the bare id") {
		t.Errorf("report missing the bare-id note:\n%s", out.String())
	}
}

// A task whose branch predates the slug default keeps that branch: git
// cannot hold GH-1 and GH-1/<slug> at once, and the existing one is the
// work. The title is not even mentioned.
func TestStartReusesABareBranchOverTheSlug(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", reopened)

	var out bytes.Buffer

	err := start(
		t.Context(),
		&out,
		startInput{main: "/code/repo", task: gh1, title: titled("", errors.New("gh: offline"))},
	)
	if err != nil {
		t.Fatal(err)
	}

	if f.called("wt ") {
		t.Error("a worktree was created although the task's branch has one")
	}

	if strings.Contains(out.String(), "bare id") {
		t.Errorf("the note explains a name that was not given:\n%s", out.String())
	}
}

// A worktree that exists but is not open in Herdr gets a fresh agent.
func TestStartOpensAClosedWorktree(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", nil), "{}")

	if err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1}); err != nil {
		t.Fatal(err)
	}

	if !f.called("herdr agent prompt gh-1") {
		t.Error("the agent was not prompted")
	}
}

// An existing branch without a checkout gets one, without --create.
func TestStartChecksOutAnExistingBranch(t *testing.T) {
	f := newFakes(t)
	f.script(refsLocal, heads("GH-1/slug"))
	f.script(worktrees, porcelain(), porcelain("GH-1/slug", "/code/repo.GH-1-slug"))
	f.script("git worktree add /code/repo.GH-1-slug GH-1/slug", "")
	f.script("herdr worktree open --path /code/repo.GH-1-slug --label GH-1/slug", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1", nil), "{}")

	if err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1}); err != nil {
		t.Fatal(err)
	}

	if !f.called("git worktree add /code/repo.GH-1-slug GH-1/slug") {
		t.Error("the existing branch was not checked out")
	}
}

// Outside Herdr the command refuses before any git, wt or herdr call, and
// the message points at herdr.dev.
func TestStartRefusesOutsideHerdr(t *testing.T) {
	f := newFakes(t)
	t.Setenv("HERDR_ENV", "")

	var out bytes.Buffer

	app := newApp()
	app.Writer = &out

	err := app.Run(t.Context(), []string{"f10", "start", "42"})
	if !errors.Is(err, herdr.ErrNotInside) {
		t.Fatalf("err = %v, want ErrNotInside", err)
	}

	if !strings.Contains(err.Error(), "https://herdr.dev") {
		t.Errorf("the refusal does not name herdr.dev: %v", err)
	}

	if len(f.calls) != 0 {
		t.Errorf("commands ran before the refusal: %v", f.calls)
	}
}

func TestStartModesAreExclusive(t *testing.T) {
	newFakes(t)

	err := newApp().Run(t.Context(), []string{"f10", "start", "--plan", "--local", "42"})
	if err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Errorf("err = %v, want the flags refused together", err)
	}
}

func TestStartBaseAndAfterAreExclusive(t *testing.T) {
	f := newFakes(t)

	err := newApp().Run(t.Context(), []string{"f10", "start", "--base", "main", "--after", "7", "42"})
	if err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Errorf("err = %v, want the flags refused together", err)
	}

	if len(f.calls) != 0 {
		t.Errorf("commands ran before the refusal: %v", f.calls)
	}
}
