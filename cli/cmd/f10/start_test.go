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
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// A fakes stands in for git, wt, herdr and the driver: every call is
// recorded, every answer is scripted, and nothing is executed. The key is
// the program name and its arguments joined by spaces.
type fakes struct {
	t       *testing.T
	calls   []string
	answers map[string]shell.Result
	has     map[string]bool
}

func newFakes(t *testing.T) *fakes {
	t.Helper()

	f := &fakes{t: t, answers: map[string]shell.Result{}, has: map[string]bool{"herdr": true}}

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

	res, ok := f.answers[key]
	if !ok {
		f.t.Fatalf("unscripted call: %q\nscripted:\n  %s", key, strings.Join(f.keys(), "\n  "))
	}

	return res
}

func (f *fakes) keys() []string {
	var ks []string
	for k := range f.answers {
		ks = append(ks, k)
	}

	return ks
}

func (f *fakes) script(key, stdout string) { f.answers[key] = shell.Result{Stdout: stdout} }

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
	refsLocal  = "git for-each-ref --format=%(refname:short) refs/heads/GH-1 refs/heads/GH-1-*"
	refsOrigin = "git for-each-ref --format=%(refname:short) refs/remotes/origin/GH-1 refs/remotes/origin/GH-1-*"
	refsOn     = "git for-each-ref --format=%(refname:short) refs/heads/GH-7 refs/heads/GH-7-*"
	refsOnOrig = "git for-each-ref --format=%(refname:short) refs/remotes/origin/GH-7 refs/remotes/origin/GH-7-*"
	worktrees  = "git worktree list --porcelain"
	opened     = `{"result":{"workspace":{"workspace_id":"ws:3"},"root_pane":{"pane_id":"pane:7"}}}`
)

var gh1 = ref.Ref{ID: "GH-1", Number: "1", Origin: ref.OriginExplicit}

// porcelain is a worktree listing of main plus the given branch/path pairs.
func porcelain(pairs ...string) string {
	var b strings.Builder

	b.WriteString("worktree /code/repo\nHEAD a\nbranch refs/heads/main\n")

	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString("\nworktree " + pairs[i+1] + "\nHEAD b\nbranch refs/heads/" + pairs[i] + "\n")
	}

	return b.String()
}

func TestBranchName(t *testing.T) {
	cases := []struct {
		name   string
		driver bool
		answer *shell.Result
		suffix string
		want   string
	}{
		{name: "no driver is the bare id", want: "GH-1"},
		{
			name:   "driver names the branch",
			driver: true,
			answer: &shell.Result{Stdout: "GH-1/short-slug\n"},
			want:   "GH-1/short-slug",
		},
		{name: "driver declining falls back", driver: true, answer: &shell.Result{Code: 3}, want: "GH-1"},
		{
			name:   "suffix follows the driver's name",
			driver: true,
			answer: &shell.Result{Stdout: "GH-1/slug"},
			suffix: "v2",
			want:   "GH-1/slug-v2",
		},
		{name: "suffix follows the bare id", suffix: "attempt2", want: "GH-1-attempt2"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakes(t)

			var d *driver.Driver
			if c.driver {
				d = fakeDriver(t)
				f.answers["driver branch 1"] = *c.answer
			}

			got, err := branchName(t.Context(), d, gh1, c.suffix)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("branchName = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBranchNameDriverFailure(t *testing.T) {
	f := newFakes(t)
	f.answers["driver branch 1"] = shell.Result{Code: 1, Stderr: "tracker down"}

	_, err := branchName(t.Context(), fakeDriver(t), gh1, "")
	if err == nil || !strings.Contains(err.Error(), "tracker down") {
		t.Errorf("err = %v, want the driver's stderr", err)
	}
}

func TestBaseBranch(t *testing.T) {
	t.Run("local branch wins", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsOn, "GH-7/thing")

		got, err := baseBranch(t.Context(), "/code/repo", "GH-7")
		if err != nil || got != "GH-7/thing" {
			t.Errorf("baseBranch = %q, %v", got, err)
		}

		if f.called("git for-each-ref --format=%(refname:short) refs/remotes") {
			t.Error("origin was searched although a local branch matched")
		}
	})

	t.Run("origin is the fallback", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsOn, "")
		f.script(refsOnOrig, "origin/GH-7")

		got, err := baseBranch(t.Context(), "/code/repo", "GH-7")
		if err != nil || got != "origin/GH-7" {
			t.Errorf("baseBranch = %q, %v", got, err)
		}
	})

	t.Run("nothing anywhere fails", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsOn, "")
		f.script(refsOnOrig, "")

		if _, err := baseBranch(t.Context(), "/code/repo", "GH-7"); err == nil {
			t.Error("baseBranch found a base where none exists")
		}
	})

	t.Run("several is an error naming them", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsOn, "GH-7\nGH-7-attempt2")

		_, err := baseBranch(t.Context(), "/code/repo", "GH-7")
		if err == nil || !strings.Contains(err.Error(), "GH-7-attempt2") {
			t.Errorf("err = %v, want the candidates named", err)
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
		{name: "the intended name wins", refs: "GH-1-attempt2\nGH-1", want: "GH-1", got: "GH-1"},
		{
			name: "one task branch is reused whatever its shape",
			refs: "GH-1/old-slug",
			want: "GH-1/new-slug",
			got:  "GH-1/old-slug",
		},
		{
			name:   "a suffix narrows",
			refs:   "GH-1\nGH-1-attempt2\nGH-1/slug-attempt2",
			want:   "GH-1-attempt2",
			suffix: "attempt2",
			got:    "GH-1-attempt2",
		},
		{name: "a suffix with no match creates", refs: "GH-1\nGH-1-attempt2", want: "GH-1-v3", suffix: "v3", got: ""},
		{name: "several without the intended name", refs: "GH-1/a\nGH-1/b", want: "GH-1", wantErr: true},
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
		modeDefault: "/f10:plan GH-1 && /f10:ship GH-1 && take defaults for all gaps; we review them at the end",
		modePlan:    "/f10:plan GH-1",
		modeLocal:   "/f10:plan GH-1 && /f10:ship local: GH-1 && take defaults for all gaps; we review them at the end",
	}

	for mode, want := range cases {
		if got := prompt(mode, "GH-1"); got != want {
			t.Errorf("prompt(%q) = %q, want %q", mode, got, want)
		}
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

// The whole flow with worktrunk present: wt creates, git is asked for the
// path, herdr opens, starts and prompts, and the report carries what the
// issue asks for.
func TestStartWithWorktrunk(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.answers[worktrees] = shell.Result{Stdout: porcelain()}
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1"), "{}")

	// after wt ran, git knows the new worktree
	calls := 0
	prev := gitx.Exec
	gitx.Exec = func(ctx context.Context, dir string, args ...string) (string, error) {
		if strings.Join(args, " ") == "worktree list --porcelain" {
			calls++
			if calls > 1 {
				f.answers[worktrees] = shell.Result{Stdout: porcelain("GH-1", "/code/repo.GH-1")}
			}
		}

		return prev(ctx, dir, args...)
	}

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

	if f.called("herdr agent prompt gh-1 ") && strings.Contains(strings.Join(f.calls, "\n"), "--wait") {
		t.Error("the prompt waited on the agent")
	}
}

// Without worktrunk, git creates the worktree at the sibling path from the
// default branch, and --on swaps that base for the task's branch.
func TestStartWithGitAlone(t *testing.T) {
	f := newFakes(t)
	f.script(refsLocal, "")
	f.script(refsOn, "GH-7/base")
	f.answers[worktrees] = shell.Result{Stdout: porcelain("GH-1", "/code/repo.GH-1")}
	f.script("git worktree add -b GH-1 /code/repo.GH-1 GH-7/base", "")
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeLocal, "GH-1"), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, mode: modeLocal, on: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if f.called("wt ") {
		t.Error("wt was run although it is not installed")
	}

	if f.called("git symbolic-ref") {
		t.Error("the default branch was looked up although --on named the base")
	}
}

// A second run for the same task opens the worktree that exists and does
// not fail; --on is noted as ignored since the branch keeps its base.
func TestStartReusesTheWorktree(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "GH-1")
	f.script(refsOn, "GH-7")
	f.answers[worktrees] = shell.Result{Stdout: porcelain("GH-1", "/code/repo.GH-1")}
	f.script("herdr worktree open --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modePlan, "GH-1"), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, mode: modePlan, on: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if f.called("wt ") || f.called("git worktree add") {
		t.Error("a worktree was created for a branch that has one")
	}

	for _, want := range []string{"reused the existing worktree", "--on ignored"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// An existing branch without a checkout gets one, without --create.
func TestStartChecksOutAnExistingBranch(t *testing.T) {
	f := newFakes(t)
	f.script(refsLocal, "GH-1/slug")
	f.answers[worktrees] = shell.Result{Stdout: porcelain("GH-1/slug", "/code/repo.GH-1-slug")}
	f.script("git worktree add /code/repo.GH-1-slug GH-1/slug", "")
	f.script("herdr worktree open --path /code/repo.GH-1-slug --label GH-1/slug", opened)
	f.script("herdr agent start gh-1 --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt gh-1 "+prompt(modeDefault, "GH-1"), "{}")

	// the listing before creation must not show the branch yet
	first := true
	prev := gitx.Exec
	gitx.Exec = func(ctx context.Context, dir string, args ...string) (string, error) {
		if first && strings.Join(args, " ") == "worktree list --porcelain" {
			first = false

			f.calls = append(f.calls, "git "+strings.Join(args, " "))

			return porcelain(), nil
		}

		return prev(ctx, dir, args...)
	}

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
