package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// The fixture every finish test starts from: GH-1's branch is checked out
// at <tmp>/repo.GH-1 beside the main checkout at <tmp>/repo, both real
// directories so the plan move and the cwd check run against disk.
type finishFixture struct {
	f    *fakes
	main string
	wt   string
	in   finishInput
}

const (
	ghView  = "gh pr view GH-1 --json state,url,number,baseRefName,mergeStateStatus,mergeCommit"
	ghRepo  = "gh repo view --json squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed"
	ghMerge = "gh pr merge GH-1 --squash"
	ghDel   = "gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1"
	cfgDeps = `git config --get-regexp ^branch\..*\.f10-after-branch$`
	lsRem   = "git ls-remote --heads origin GH-1"
	curBr   = "git branch --show-current"
	wsList  = "herdr workspace list"

	prOpen   = `{"state":"OPEN","url":"https://gh/pr/9","number":9,"baseRefName":"main","mergeCommit":null}`
	prMerged = `{"state":"MERGED","url":"https://gh/pr/9","number":9,"baseRefName":"main","mergeCommit":{"oid":"m3rge"}}`
	allowAll = `{"squashMergeAllowed":true,"mergeCommitAllowed":true,"rebaseMergeAllowed":true}`
)

func newFinishFixture(t *testing.T) *finishFixture {
	t.Helper()

	root := t.TempDir()
	fx := &finishFixture{f: newFakes(t), main: filepath.Join(root, "repo"), wt: filepath.Join(root, "repo.GH-1")}

	for _, d := range []string{fx.main, filepath.Join(fx.wt, ".f10", "plans")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	fx.in = finishInput{main: fx.main, task: gh1, host: host{name: "gh", dir: fx.main}, cwd: fx.main}

	f := fx.f
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", fx.wt))
	f.script("git status --porcelain", "")
	f.script(wsList, fx.workspaces(true))
	f.script(headRef, "origin/main")
	f.script(curBr, "main")
	f.script("git pull --ff-only", "")
	f.script("git merge-base --is-ancestor m3rge main", "")
	f.script(cfgDeps, "")
	f.script(lsRem, "abc\trefs/heads/GH-1")
	f.script(ghDel, "")
	f.script("herdr workspace close ws:9", "{}")
	f.script("git worktree remove "+fx.wt, "")
	f.script("git branch -D GH-1", "")

	return fx
}

// workspaces is a listing with main's workspace and, when open, the task's.
func (fx *finishFixture) workspaces(open bool) string {
	main := `{"workspace_id":"ws:1","label":"repo","focused":true,"worktree":{"checkout_path":"` + fx.main + `"}}`
	if !open {
		return `{"result":{"workspaces":[` + main + `]}}`
	}

	task := `{"workspace_id":"ws:9","label":"GH-1","agent_status":"idle","worktree":{"checkout_path":"` + fx.wt + `"}}`

	return `{"result":{"workspaces":[` + main + `,` + task + `]}}`
}

func (fx *finishFixture) plan(t *testing.T, rel, body string) string {
	t.Helper()

	p := filepath.Join(fx.wt, ".f10", "plans", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	return p
}

// openPR scripts the host side of a mergeable PR on GitHub.
func (fx *finishFixture) openPR() {
	fx.f.script(ghView, prOpen, prMerged)
	fx.f.script(ghRepo, allowAll)
	fx.f.script(ghMerge, "")
}

func (fx *finishFixture) run(t *testing.T) (string, error) {
	t.Helper()

	var out bytes.Buffer

	err := finish(t.Context(), &out, fx.in)

	return out.String(), err
}

// The happy path from main's checkout: view, merge, view again, delete the
// remote branch, pull, confirm, archive, report, close, remove, delete.
func TestFinishMergesAndCleansUp(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	plan := fx.plan(t, "GH-1.md", "# plan")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	archived := filepath.Join(fx.main, ".f10", "plans", "archive", "GH-1.md")

	for _, want := range []string{
		"task:", "GH-1", "pr:", "↗ https://gh/pr/9", "merge:", "m3rge", "plan:", archived,
		"workspace:", "ws:9", "path:", fx.wt,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}

	if _, err := os.Stat(plan); !errors.Is(err, os.ErrNotExist) {
		t.Error("the plan was left in the worktree")
	}

	if body, err := os.ReadFile(archived); err != nil || string(body) != "# plan" {
		t.Errorf("archived plan = %q, %v", body, err)
	}

	order := []string{
		ghView,
		ghMerge,
		ghView,
		lsRem,
		ghDel,
		curBr,
		"git pull --ff-only",
		"git merge-base --is-ancestor m3rge main",
		"git worktree remove " + fx.wt,
		"git branch -D GH-1",
		"herdr workspace close ws:9",
	}

	last := -1

	for _, step := range order {
		idx := indexAfter(fx.f.calls, step, last)
		if idx < 0 {
			t.Fatalf("%q did not run after step %d:\n%s", step, last, strings.Join(fx.f.calls, "\n"))
		}

		last = idx
	}

	if fx.f.called("herdr workspace focus") {
		t.Error("main's workspace was focused although the command ran from it")
	}

	if strings.Contains(strings.Join(fx.f.calls, "\n"), "--admin") {
		t.Error("the merge used --admin")
	}

	// the repo allows every method and project.md declares none, so the
	// squash default is said rather than taken in silence
	if !strings.Contains(out, "merged with --squash: project.md declares no merge method") {
		t.Errorf("the default merge method was not noted:\n%s", out)
	}
}

// A branch another task started with --after on this one loses its
// dependency once this one merged: both keys go, the note says so, and a
// dependency on some other branch is left alone.
func TestFinishReleasesDependents(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.script(cfgDeps, "branch.GH-8/slug.f10-after-branch GH-1\nbranch.GH-9.f10-after-branch GH-2")
	fx.f.script("git config --unset branch.GH-8/slug.f10-after", "")
	fx.f.script("git config --unset branch.GH-8/slug.f10-after-branch", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"git config --unset branch.GH-8/slug.f10-after",
		"git config --unset branch.GH-8/slug.f10-after-branch",
	} {
		if !fx.f.called(want) {
			t.Errorf("%q did not run: %v", want, fx.f.calls)
		}
	}

	if fx.f.called("git config --unset branch.GH-9") {
		t.Error("a dependency on another branch was cleared")
	}

	if !strings.Contains(
		out,
		"GH-8/slug depended on GH-1: the dependency is cleared, its ship and pr target main now",
	) {
		t.Errorf("the release note is missing:\n%s", out)
	}
}

// A plan that cannot leave the worktree stops finish before the worktree
// goes: with in-repo storage the worktree holds the only copy.
func TestFinishStopsWhenThePlanCannotBeArchived(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.plan(t, "GH-1.md", "# plan")

	// the archive dir's path is taken by a file, so it cannot be created
	if err := os.MkdirAll(filepath.Join(fx.main, ".f10", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(fx.main, ".f10", "plans", "archive"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "archiving the plan at") {
		t.Fatalf("err = %v, want the archive failure", err)
	}

	for _, ran := range []string{"herdr workspace close", "git worktree remove", "git branch -D", "wt remove"} {
		if fx.f.called(ran) {
			t.Errorf("%q ran although the plan was not archived", ran)
		}
	}
}

func indexAfter(calls []string, want string, after int) int {
	for i := after + 1; i < len(calls); i++ {
		if calls[i] == want {
			return i
		}
	}

	return -1
}

// An already merged PR is confirmed, not merged again, and the remote
// branch is deleted only when origin still has it.
func TestFinishAlreadyMerged(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, prMerged)
	fx.f.script(lsRem, "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if fx.f.called("gh pr merge") || fx.f.called(ghDel) {
		t.Errorf("merged or deleted again: %v", fx.f.calls)
	}

	for _, want := range []string{"was already merged", "no plan at", "nothing archived"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

// The host's refusal is the error, and nothing local moves after it.
func TestFinishStopsOnHostRefusal(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, prOpen)
	fx.f.script(ghRepo, allowAll)
	fx.f.fail(
		ghMerge,
		shell.Result{
			Code:   1,
			Stderr: "X Pull request #9 is not mergeable: the base branch policy prohibits the merge.",
		},
	)
	fx.plan(t, "GH-1.md", "# plan")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "base branch policy prohibits the merge") {
		t.Fatalf("err = %v, want the host's reason", err)
	}

	for _, p := range []string{"git pull", "git fetch", "herdr workspace close", "git worktree remove", "git branch -D", ghDel} {
		if fx.f.called(p) {
			t.Errorf("%q ran after the host refused", p)
		}
	}

	if _, err := os.Stat(filepath.Join(fx.wt, ".f10", "plans", "GH-1.md")); err != nil {
		t.Error("the plan was moved although the merge failed")
	}
}

func TestFinishRefusesClosedPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, `{"state":"CLOSED","url":"https://gh/pr/9","baseRefName":"main"}`)

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "closed without a merge") {
		t.Fatalf("err = %v, want a refusal on the closed PR", err)
	}

	if fx.f.called("gh pr merge") {
		t.Error("a closed PR was merged")
	}
}

// A PR stacked on another task's branch waits for that task.
func TestFinishRefusesStackedPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, `{"state":"OPEN","url":"https://gh/pr/9","baseRefName":"GH-7/base"}`)

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "stacked on GH-7/base") {
		t.Fatalf("err = %v, want the stacked refusal", err)
	}

	if fx.f.called("gh pr merge") || fx.f.called(ghRepo) {
		t.Error("the stacked PR was merged")
	}
}

// A dirty worktree is named file by file, before the host is reached.
func TestFinishRefusesDirtyWorktree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script("git status --porcelain", " M cli/cmd/f10/finish.go\n?? notes.txt")

	_, err := fx.run(t)
	if err == nil {
		t.Fatal("a dirty worktree was finished")
	}

	for _, want := range []string{"uncommitted changes", " M cli/cmd/f10/finish.go", "?? notes.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to carry %q", err, want)
		}
	}

	if fx.f.called("gh ") || fx.f.called("herdr ") {
		t.Errorf("the host or herdr was reached before the dirty check: %v", fx.f.calls)
	}
}

// From inside the worktree the command refuses without --yes; with it, the
// report precedes the focus on main's workspace and the close.
func TestFinishSelfClose(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.cwd = filepath.Join(fx.wt, "cli")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "would close its own workspace (GH-1)") {
		t.Fatalf("err = %v, want the self-close refusal", err)
	}

	if fx.f.called("gh ") {
		t.Error("the host was reached before the self-close refusal")
	}

	fx = newFinishFixture(t)
	fx.in.cwd = filepath.Join(fx.wt, "cli")
	fx.in.yes = true
	fx.openPR()
	fx.f.script("herdr workspace focus ws:1", "{}")

	// a writer that records which herdr calls had run when the report landed
	rec := &recorder{f: fx.f}

	if err := finish(t.Context(), rec, fx.in); err != nil {
		t.Fatal(err)
	}

	if rec.sawFocus || rec.sawClose {
		t.Error("the workspace was focused or closed before the report was printed")
	}

	focus, closeIdx := indexAfter(
		fx.f.calls,
		"herdr workspace focus ws:1",
		-1,
	), indexAfter(
		fx.f.calls,
		"herdr workspace close ws:9",
		-1,
	)
	if focus < 0 || closeIdx < 0 || focus > closeIdx {
		t.Errorf("focus then close expected, calls: %v", fx.f.calls)
	}

	if !strings.Contains(rec.buf.String(), "this workspace closes after this report") {
		t.Errorf("the self-close note is missing:\n%s", rec.buf.String())
	}
}

// recorder notes, at the moment the report is written, whether the
// workspace calls had already run.
type recorder struct {
	f        *fakes
	buf      bytes.Buffer
	sawFocus bool
	sawClose bool
}

func (r *recorder) Write(p []byte) (int, error) {
	r.sawFocus = r.sawFocus || r.f.called("herdr workspace focus")
	r.sawClose = r.sawClose || r.f.called("herdr workspace close")

	return r.buf.Write(p)
}

// HERDR_WORKSPACE_ID naming the task's workspace is the self-close case
// too, wherever the shell has cd'd to.
func TestFinishSelfCloseByWorkspaceID(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.workspace = "ws:9"

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "would close its own workspace") {
		t.Fatalf("err = %v, want the self-close refusal", err)
	}
}

// With no workspace on the worktree there is nothing to close, and the
// report says so.
func TestFinishWithoutAWorkspace(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(wsList, fx.workspaces(false))
	fx.openPR()

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if fx.f.called("herdr workspace close") {
		t.Error("a workspace was closed although none showed the worktree")
	}

	if !strings.Contains(out, "nothing to close") || strings.Contains(out, "workspace:") {
		t.Errorf("report:\n%s", out)
	}
}

// Main parked on another branch gets the default branch fetched into,
// not a pull, and the report says main's checkout was left alone.
func TestFinishFetchesWhenMainIsParked(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(curBr, "spike/thing")
	fx.f.script("git fetch origin main:main", "")
	fx.openPR()

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if fx.f.called("git pull") || !fx.f.called("git fetch origin main:main") {
		t.Errorf("calls: %v", fx.f.calls)
	}

	if !strings.Contains(out, "main updated by fetch") {
		t.Errorf("report:\n%s", out)
	}
}

// A merge the local default branch does not contain stops everything local.
func TestFinishStopsWhenMergeIsNotInMain(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.fail("git merge-base --is-ancestor m3rge main", shell.Result{Code: 1})
	fx.openPR()
	fx.plan(t, "GH-1.md", "# plan")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "not in main after pulling") {
		t.Fatalf("err = %v", err)
	}

	for _, p := range []string{"herdr workspace close", "git worktree remove", "git branch -D"} {
		if fx.f.called(p) {
			t.Errorf("%q ran although the merge is not in main", p)
		}
	}

	if _, err := os.Stat(filepath.Join(fx.wt, ".f10", "plans", "GH-1.md")); err != nil {
		t.Error("the plan was moved although the merge is not in main")
	}
}

// Archive numbering: an archived GH-1.md already in main makes this one
// GH-1.1.md, and the worktree's own archived versions move along first.
func TestFinishArchiveNumbering(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.plan(t, "GH-1.md", "current")
	fx.plan(t, filepath.Join("archive", "GH-1.1.md"), "older")

	archive := filepath.Join(fx.main, ".f10", "plans", "archive")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(archive, "GH-1.md"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"GH-1.md": "first", "GH-1.1.md": "older", "GH-1.2.md": "current"} {
		got, err := os.ReadFile(filepath.Join(archive, name))
		if err != nil || string(got) != body {
			t.Errorf("%s = %q, %v; want %q", name, got, err, body)
		}
	}

	if !strings.Contains(out, filepath.Join(archive, "GH-1.2.md")) {
		t.Errorf("report does not name the archived plan:\n%s", out)
	}
}

// Out-of-tree storage: the plan moves within the shared root.
func TestFinishArchivesOutOfTree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()

	shared := t.TempDir()
	fx.in.plansDir = shared

	if err := os.WriteFile(filepath.Join(shared, "GH-1.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(shared, "archive", "GH-1.md")); err != nil {
		t.Errorf("plan not archived in the shared root: %v", err)
	}

	if _, err := os.Stat(filepath.Join(fx.main, ".f10")); !errors.Is(err, os.ErrNotExist) {
		t.Error("an in-repo .f10/ was created under out-of-tree storage")
	}
}

// With worktrunk on PATH the removal goes through wt and deletes the branch.
func TestFinishRemovesThroughWorktrunk(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.has["wt"] = true
	fx.f.script("wt remove --yes --force-delete --foreground --format json GH-1", "{}")
	fx.openPR()

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if fx.f.called("git worktree remove") || fx.f.called("git branch -D") {
		t.Error("git removed the worktree although wt is installed")
	}
}

// The GitLab path: glab's spellings, squash from the project's option,
// auto-merge off, the source branch removed by the merge itself.
func TestFinishOnGitLab(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.host = host{name: "glab", dir: fx.main}
	fx.f.script(
		"glab mr view GH-1 -F json",
		`{"state":"opened","web_url":"https://gl/mr/4","iid":4,"target_branch":"main"}`,
		`{"state":"merged","web_url":"https://gl/mr/4","iid":4,"target_branch":"main","squash_commit_sha":"m3rge","merge_commit_sha":"other"}`,
	)
	fx.f.script("glab repo view -F json", `{"squash_option":"default_on"}`)
	fx.f.script("glab mr merge GH-1 --remove-source-branch --auto-merge=false --yes --squash", "")
	fx.f.script(lsRem, "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !fx.f.called("glab mr merge GH-1 --remove-source-branch --auto-merge=false --yes --squash") {
		t.Errorf("calls: %v", fx.f.calls)
	}

	if fx.f.called("glab api") {
		t.Error("the remote branch was deleted although the merge removed it")
	}

	if !strings.Contains(out, "↗ https://gl/mr/4") || !strings.Contains(out, "m3rge") {
		t.Errorf("report:\n%s", out)
	}
}

func TestFinishRefusesWithoutBranchOrWorktree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(refsLocal, "")

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "has no branch here") {
		t.Errorf("err = %v", err)
	}

	fx = newFinishFixture(t)
	fx.f.script(worktrees, porcelain())

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "has no worktree here") {
		t.Errorf("err = %v", err)
	}
}

func TestFinishRefusesOutsideHerdr(t *testing.T) {
	f := newFakes(t)
	t.Setenv("HERDR_ENV", "")

	app := newApp()
	app.Writer = &bytes.Buffer{}

	err := app.Run(t.Context(), []string{"f10", "finish", "42"})
	if !errors.Is(err, herdr.ErrNotInside) {
		t.Fatalf("err = %v, want ErrNotInside", err)
	}

	if len(f.calls) != 0 {
		t.Errorf("commands ran before the refusal: %v", f.calls)
	}
}

func TestMergeMethod(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		hosting string
		repo    string
		want    string
		noted   bool // the method was a default worth saying
	}{
		{"declared squash", "gh", "github.com. Merge method: squash.", "", "--squash", false},
		{"declared rebase, any case", "gh", "merge: REBASE", "", "--rebase", false},
		{"declared merge on gitlab is plain", "glab", "merge method: merge", "", "", false},
		{
			"one allowed",
			"gh",
			"",
			`{"squashMergeAllowed":false,"mergeCommitAllowed":false,"rebaseMergeAllowed":true}`,
			"--rebase",
			false,
		},
		{"several allowed", "gh", "", allowAll, "--squash", true},
		{"gitlab squash always", "glab", "", `{"squash_option":"always"}`, "--squash", false},
		{"gitlab squash off", "glab", "", `{"squash_option":"never"}`, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakes(t)
			f.script(ghRepo, c.repo)
			f.script("glab repo view -F json", c.repo)

			got, note, err := host{name: c.host, dir: "/repo"}.mergeMethod(t.Context(), c.hosting)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("mergeMethod = %q, want %q", got, c.want)
			}

			if (note != "") != c.noted {
				t.Errorf("note = %q, noted = %v", note, c.noted)
			}

			if c.hosting != "" && f.called(c.host+" repo view") {
				t.Error("the repo was asked although project.md declares the method")
			}
		})
	}
}

func TestInside(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "repo.GH-1")

	if err := os.MkdirAll(filepath.Join(wt, "cli"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		dir  string
		want bool
	}{
		{wt, true},
		{filepath.Join(wt, "cli"), true},
		{root, false},
		{filepath.Join(root, "repo.GH-10"), false},
		{filepath.Join(root, "repo"), false},
	}

	for _, c := range cases {
		if got := inside(c.dir, wt); got != c.want {
			t.Errorf("inside(%s, %s) = %v, want %v", c.dir, wt, got, c.want)
		}
	}
}
