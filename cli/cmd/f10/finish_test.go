package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
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
	ghView  = "gh pr view GH-1 --json state,url,number,title,body,baseRefName,mergeStateStatus,mergeCommit,author"
	ghRepo  = "gh repo view --json squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed"
	ghMerge = "gh pr merge GH-1 --squash"
	ghDel   = "gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1"
	ghStack = "gh pr list --base GH-1 --state open --limit 1000 --json number,url,headRefName"
	cfgDeps = `git config --get-regexp ^branch\..*\.f10-after-branch$`
	lsRem   = "git ls-remote --heads origin GH-1"
	// finish drops the local copy of origin's branch along with the branch
	dropRemoteRef = "git update-ref -d refs/remotes/origin/GH-1"
	curBr         = "git branch --show-current"
	wsList        = "herdr workspace list"

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

	fx.in = finishInput{main: fx.main, task: gh1, host: newHost("gh", fx.main), cwd: fx.main}

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
	f.script(ghStack, "[]")
	f.script(ghDel, "")
	f.script(dropRemoteRef, "")
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

// With traces on, the squash commit's body is the PR's description followed
// by the task's trailer, so the commit on the default branch still names
// the task; a rebase passes no body, since the branch commits keep theirs.
// The PR body's state block says finish before the merge and never reaches
// the commit.
func TestFinishSquashBodyKeepsTheTask(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.traces = true
	fx.in.method = "squash"
	fx.plan(t, "GH-1.md", "# plan")

	open := `{"state":"OPEN","url":"https://gh/pr/9","number":9,"baseRefName":"main","body":"stale"}`
	current := `{"body":"Adds the thing.\n\nCloses #1\n\n<!-- state {\"v\":1,\"task\":\"GH-1\",\"step\":\"judge\",\"round\":1} -->\n"}`
	merge := "gh pr merge GH-1 --squash --body Adds the thing.\n\nCloses #1\n\nTask: GH-1\n"
	fx.f.script(ghView, open, prMerged)
	fx.f.script("gh pr view GH-1 --json body", current)
	fx.f.absent = append(fx.f.absent, "gh pr edit GH-1 --body-file ", "git config --get branch.GH-1.f10-after")
	fx.f.script(merge, "")
	edited := captureBodyEdits(t)

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.called(merge) {
		t.Errorf("the squash carried no body:\n%s", strings.Join(fx.f.calls, "\n"))
	}

	want := "Adds the thing.\n\nCloses #1\n\n<!-- state {\"v\":1,\"task\":\"GH-1\",\"step\":\"finish\",\"round\":1} -->\n"
	if *edited != want {
		t.Errorf("the PR body became %q, want %q", *edited, want)
	}
}

// captureBodyEdits reads the body each `gh pr edit --body-file` carries,
// which the fakes see only as a temp path, before the file is removed.
func captureBodyEdits(t *testing.T) *string {
	t.Helper()

	var last string

	prev := shell.Capture
	shell.Capture = func(ctx context.Context, dir, name string, args ...string) (shell.Result, error) {
		if name == "gh" && len(args) > 2 && args[0] == "pr" && args[1] == "edit" {
			data, err := os.ReadFile(args[len(args)-1])
			if err != nil {
				t.Fatal(err)
			}

			last = string(data)
		}

		return prev(ctx, dir, name, args...)
	}

	t.Cleanup(func() { shell.Capture = prev })

	return &last
}

// A body the host will not hand over or take back is a note on the report;
// the merge still runs, with the description finish read first.
func TestFinishStateBlockFailureIsANote(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.traces = true
	fx.in.method = "squash"
	fx.plan(t, "GH-1.md", "# plan")

	open := `{"state":"OPEN","url":"https://gh/pr/9","number":9,"baseRefName":"main","body":"Adds the thing.\n"}`
	merge := "gh pr merge GH-1 --squash --body Adds the thing.\n\nTask: GH-1\n"
	fx.f.script(ghView, open, prMerged)
	fx.f.answers["gh pr view GH-1 --json body"] = []shell.Result{{Code: 1, Stderr: "rate limited"}}
	fx.f.script(merge, "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !fx.f.called(merge) || !strings.Contains(out, "state block was not rewritten: gh: rate limited") {
		t.Errorf("merge called %v, report:\n%s", fx.f.called(merge), out)
	}
}

// A bundle's body was just rewritten to keep a rider open, so a body the
// host will not hand back stops the finish: the stale one would close it.
func TestFinishStateBlockFailureStopsABundle(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.traces = true
	fx.in.method = "squash"
	fx.in.task, fx.in.close = bundle12, []string{}
	fx.f.answers["gh pr view GH-1 --json body"] = []shell.Result{
		{Stdout: `{"body":"Closes #1\nCloses #2"}`},
		{Code: 1, Stderr: "rate limited"},
	}
	fx.f.absent = append(fx.f.absent, "gh pr edit GH-1 --body-file ")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err = %v, want the host's words", err)
	}

	if fx.f.called("gh pr merge") {
		t.Errorf("merged on a stale body:\n%s", strings.Join(fx.f.calls, "\n"))
	}
}

func TestMergeBody(t *testing.T) {
	bundle := finishInput{traces: true, task: ref.Ref{ID: "GH-1", Bundle: []string{"GH-1", "GH-2"}}}
	described := "  Adds the thing.\n"

	cases := []struct {
		name        string
		in          finishInput
		description string
		method      string
		want        string
	}{
		{"off", finishInput{task: gh1}, described, "--squash", ""},
		{"rebase", finishInput{traces: true, task: gh1}, described, "--rebase", ""},
		{"glab plain merge", finishInput{traces: true, task: gh1}, described, "", ""},
		{"squash", finishInput{traces: true, task: gh1}, described, "--squash", "Adds the thing.\n\nTask: GH-1\n"},
		{"merge commit", finishInput{traces: true, task: gh1}, described, "--merge", "Adds the thing.\n\nTask: GH-1\n"},
		{"empty description", finishInput{traces: true, task: gh1}, "", "--squash", "Task: GH-1\n"},
		{
			"state block stripped",
			finishInput{traces: true, task: gh1},
			"## Summary\n\n<!-- state {\"v\":1,\"step\":\"finish\"} -->\n\nAdds the thing.\n", "--squash",
			"## Summary\n\nAdds the thing.\n\nTask: GH-1\n",
		},
		{"bundle", bundle, "", "--squash", "Task: GH-1\nTask: GH-2\n"},
	}

	for _, c := range cases {
		if got := mergeBody(c.in, c.description, c.method); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A kept rider's Closes was rewritten to Part of just before the merge, so
// the squash body is read again from the host: the commit on the default
// branch must not close what the PR body no longer closes.
func TestFinishSquashBodyFollowsTheKeptRider(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.traces = true
	fx.in.method = "squash"
	fx.in.task, fx.in.close = bundle12, []string{}
	fx.f.script("gh pr view GH-1 --json body", `{"body":"Closes #1\nCloses #2"}`, `{"body":"Closes #1\nPart of #2"}`)
	fx.f.absent = append(fx.f.absent, "gh pr edit GH-1 --body-file ", "git config --get branch.GH-1.f10-after")

	merge := "gh pr merge GH-1 --squash --body Closes #1\nPart of #2\n\nTask: GH-1\nTask: GH-2\n"
	fx.f.script(merge, "")

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.called(merge) {
		t.Errorf("the squash body kept a stale Closes:\n%s", strings.Join(fx.f.calls, "\n"))
	}
}

// glab takes the whole message, so the title goes on top of the body.
func TestMergePullGlabSquashMessage(t *testing.T) {
	f := newFakes(t)
	h := newHost("glab", t.TempDir())
	view := "glab mr view GH-1 -F json"
	merge := "glab mr merge GH-1 --remove-source-branch --auto-merge=false --yes --squash --squash-message Add the thing\n\nTask: GH-1\n"
	f.script(merge, "")
	f.script(view, `{"state":"merged","web_url":"https://gl/mr/9","squash_commit_sha":"sq"}`)

	p, err := h.mergePull(t.Context(), pull{Title: "Add the thing"}, "GH-1", "--squash", "Task: GH-1\n")
	if err != nil || p.sha() != "sq" {
		t.Fatalf("merged = %+v, %v", p, err)
	}

	if !f.called(merge) {
		t.Errorf("calls:\n%s", strings.Join(f.calls, "\n"))
	}
}

// The happy path from main's checkout: view, merge, view again, delete the
// remote branch, pull, confirm, archive, report, remove, delete, close.
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
		dropRemoteRef,
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
// dependency once this one merged: both keys go, the branch is marked
// landed with the base task's id so its next ship or catchup brings main
// in, the note says so, and a dependency on some other branch is left
// alone.
func TestFinishReleasesDependents(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.script(cfgDeps, "branch.GH-8/slug.f10-after-branch GH-1\nbranch.GH-9.f10-after-branch GH-2")
	fx.f.script("git config --get branch.GH-8/slug.f10-after", "GH-1")
	fx.f.script("git config --unset branch.GH-8/slug.f10-after", "")
	fx.f.script("git config --unset branch.GH-8/slug.f10-after-branch", "")
	fx.f.script("git config branch.GH-8/slug.f10-landed GH-1", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"git config --unset branch.GH-8/slug.f10-after",
		"git config --unset branch.GH-8/slug.f10-after-branch",
		"git config branch.GH-8/slug.f10-landed GH-1",
	} {
		if !fx.f.called(want) {
			t.Errorf("%q did not run: %v", want, fx.f.calls)
		}
	}

	if fx.f.called("git config --unset branch.GH-9") || fx.f.called("git config branch.GH-9") {
		t.Error("a dependency on another branch was touched")
	}

	if !strings.Contains(
		out,
		"GH-8/slug depended on GH-1: the dependency is cleared and its pr targets main now; "+
			"/f10:catchup, or its next ship unless catchup is off, brings main in first",
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

// A PR stacked on the branch moves to main before the branch goes: a
// refs-API delete closes every PR still based on it.
func TestFinishRetargetsStackedPRsBeforeDelete(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.script(ghStack, `[{"number":12,"url":"https://gh/pr/12","headRefName":"GH-2/next"}]`)
	fx.f.script("gh pr edit 12 --base main", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	edit, del := slices.Index(fx.f.calls, "gh pr edit 12 --base main"), slices.Index(fx.f.calls, ghDel)
	if edit < 0 || del < 0 || edit > del {
		t.Errorf("want the retarget before the delete:\n%s", strings.Join(fx.f.calls, "\n"))
	}

	if !strings.Contains(out, "retargeted https://gh/pr/12 to main") {
		t.Errorf("report missing the retarget:\n%s", out)
	}
}

// A retarget the host refuses stops finish with the branch on origin, so
// the stacked PR stays open; a rerun retries it.
func TestFinishKeepsBranchWhenRetargetFails(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.script(ghStack, `[{"number":12,"url":"https://gh/pr/12","headRefName":"GH-2/next"}]`)
	fx.f.fail("gh pr edit 12 --base main", shell.Result{Code: 1, Stderr: "GraphQL: Resource not accessible"})
	fx.plan(t, "GH-1.md", "# plan")

	_, err := fx.run(t)
	if err == nil {
		t.Fatal("finish went on after a failed retarget")
	}

	for _, want := range []string{"https://gh/pr/12", "Resource not accessible", "origin/GH-1 is left in place"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to carry %q", err, want)
		}
	}

	for _, p := range []string{ghDel, dropRemoteRef, "git worktree remove", "git branch -D"} {
		if fx.f.called(p) {
			t.Errorf("%q ran after the retarget failed", p)
		}
	}

	if _, err := os.Stat(filepath.Join(fx.wt, ".f10", "plans", "GH-1.md")); err != nil {
		t.Error("the plan was moved although the retarget failed")
	}
}

// A failed retarget after a successful one names the one already moved,
// since the error is all the run prints.
func TestFinishNamesRetargetsBeforeAFailure(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.script(ghStack, `[{"number":12,"url":"https://gh/pr/12"},{"number":13,"url":"https://gh/pr/13"}]`)
	fx.f.script("gh pr edit 12 --base main", "")
	fx.f.fail("gh pr edit 13 --base main", shell.Result{Code: 1, Stderr: "GraphQL: Resource not accessible"})

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "already retargeted: https://gh/pr/12") {
		t.Fatalf("err = %v, want it to name the PR already moved", err)
	}

	if fx.f.called(ghDel) {
		t.Error("the branch was deleted after a failed retarget")
	}
}

// A listing the host refuses keeps the branch too: an unlisted stacked PR
// would be closed by the delete.
func TestFinishKeepsBranchWhenListingFails(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.fail(ghStack, shell.Result{Code: 1, Stderr: "HTTP 502"})

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "listing the requests based on GH-1 failed") ||
		!strings.Contains(err.Error(), "origin/GH-1 is left in place") {
		t.Fatalf("err = %v, want the listing failure with the branch kept", err)
	}

	for _, p := range []string{ghDel, dropRemoteRef, "git worktree remove", "git branch -D"} {
		if fx.f.called(p) {
			t.Errorf("%q ran after the listing failed", p)
		}
	}
}

// A rerun on a PR already merged retargets what the branch still holds.
func TestFinishRetargetsAfterEarlierMerge(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, prMerged)
	fx.f.script(ghStack, `[{"number":12,"url":"https://gh/pr/12","headRefName":"GH-2/next"}]`)
	fx.f.script("gh pr edit 12 --base main", "")

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if fx.f.called("gh pr merge") || !fx.f.called("gh pr edit 12 --base main") || !fx.f.called(ghDel) {
		t.Errorf("calls:\n%s", strings.Join(fx.f.calls, "\n"))
	}
}

// On GitLab a source branch the merge left behind has its MRs retargeted
// by iid before the API deletes it.
func TestFinishRetargetsStackedMRsOnGitLab(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.host = newHost("glab", fx.main)
	fx.f.script(
		"glab mr view GH-1 -F json",
		`{"state":"merged","web_url":"https://gl/mr/4","iid":4,"target_branch":"main","squash_commit_sha":"m3rge"}`,
	)
	fx.f.script(
		"glab mr list --target-branch GH-1 --per-page 100 -F json",
		`[{"iid":7,"web_url":"https://gl/mr/7","source_branch":"GH-2/next"}]`,
	)
	fx.f.script("glab mr update 7 --target-branch main", "")
	fx.f.script("glab api -X DELETE projects/:fullpath/repository/branches/GH-1", "")

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	edit := slices.Index(fx.f.calls, "glab mr update 7 --target-branch main")
	del := slices.Index(fx.f.calls, "glab api -X DELETE projects/:fullpath/repository/branches/GH-1")

	if edit < 0 || del < 0 || edit > del {
		t.Errorf("want the retarget before the delete:\n%s", strings.Join(fx.f.calls, "\n"))
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

	root := t.TempDir()
	shared := filepath.Join(root, "plans")
	fx.in.plansDir = shared

	images := filepath.Join(root, "images", "GH-1")
	for _, d := range []string{shared, images} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(shared, "GH-1.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(shared, "archive", "GH-1.md")); err != nil {
		t.Errorf("plan not archived in the shared root: %v", err)
	}

	if _, err := os.Stat(images); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the task's images survived in the shared root: %v", err)
	}

	if _, err := os.Stat(filepath.Join(fx.main, ".f10")); !errors.Is(err, os.ErrNotExist) {
		t.Error("an in-repo .f10/ was created under out-of-tree storage")
	}
}

// With in-repo storage, images a read from main left in main's .f10/ go too.
func TestRemoveImagesInRepo(t *testing.T) {
	main := t.TempDir()

	dir := filepath.Join(main, ".f10", "images", "GH-1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	if note := removeImages(main, "", "GH-1"); note != "" {
		t.Errorf("removeImages = %q, want no note", note)
	}

	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("main's images survived: %v", err)
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
	fx.in.host = newHost("glab", fx.main)
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

	// a branch whose PR is still open is one start never made, or work not landed
	fx = newFinishFixture(t)
	fx.f.script(worktrees, porcelain())
	fx.f.script(ghView, prOpen)

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "has no worktree here") {
		t.Errorf("err = %v", err)
	}
}

// A finish that stopped after removing the worktree is rerun to the end:
// the PR merged, so the rest is cleaned up and only the branch deleted.
func TestFinishMergedWithoutWorktree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(worktrees, porcelain())
	fx.f.script(ghView, prMerged)
	fx.f.script(lsRem, "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if !fx.f.called("git branch -D GH-1") || fx.f.called("git worktree remove") ||
		fx.f.called("herdr workspace close") {
		t.Errorf("calls: %v", fx.f.calls)
	}

	for _, want := range []string{"was already merged", "had no worktree left", "https://gh/pr/9"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

// A task with no branch left and its plan archived is already finished.
func TestFinishAlreadyFinished(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(refsLocal, "")
	fx.in.plansDir = filepath.Join(t.TempDir(), "plans")

	if err := os.MkdirAll(filepath.Join(fx.in.plansDir, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(fx.in.plansDir, "archive", "GH-1.1.md"), []byte("# plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := fx.run(t)
	if err != nil || !strings.Contains(out, "already finished") {
		t.Errorf("err = %v, out:\n%s", err, out)
	}

	// a re-plan archives the old plan too, and keeps the new one live
	if err := os.WriteFile(filepath.Join(fx.in.plansDir, "GH-1.md"), []byte("# plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "nothing to finish") {
		t.Errorf("a re-planned task with a live plan: err = %v", err)
	}
}

// f10 finish refuses a task a drive holds, naming the drive.
func TestFinishLeaseRefusesADrivenTask(t *testing.T) {
	root := t.TempDir()

	held, err := drives.Acquire(root, "GH-1", 3)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := finishLease(root, "GH-1"); err == nil || !strings.Contains(err.Error(), "owned by drive 3") ||
		!strings.Contains(err.Error(), "f10 drive cancel 3") {
		t.Errorf("err = %v", err)
	}

	held.Release()

	l, err := finishLease(root, "GH-1")
	if err != nil {
		t.Fatal(err)
	}

	l.Release()
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
		name     string
		host     string
		declared string
		repo     string
		want     string
		noted    bool // the method was a default worth saying
	}{
		{"declared squash", "gh", "squash", "", "--squash", false},
		{"declared rebase, any case", "gh", "REBASE", "", "--rebase", false},
		{"declared merge on gitlab is plain", "glab", "merge", "", "", false},
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

			got, note, err := newHost(c.host, "/repo").mergeMethod(t.Context(), c.declared)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("mergeMethod = %q, want %q", got, c.want)
			}

			if (note != "") != c.noted {
				t.Errorf("note = %q, noted = %v", note, c.noted)
			}

			if c.declared != "" && f.called(c.host+" repo view") {
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

// onMain turns the fixture into a task shipped from the main checkout: the
// branch is checked out in main, and the plan sits in main's .f10/plans.
func (fx *finishFixture) onMain(t *testing.T, plan string) string {
	t.Helper()

	fx.f.script(worktrees, porcelain("GH-1", fx.main))
	fx.f.script(curBr, "GH-1")
	fx.f.script("git fetch origin main:main", "")
	fx.f.script("git switch main", "")

	p := filepath.Join(fx.main, ".f10", "plans", "GH-1.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	return p
}

// A branch checked out in main: no self-close refusal from main's cwd, the
// merge, the default branch fetched into while main stays on the task's
// branch, the plan archived within main, the report, then main switched
// and the local branch deleted, with no worktree removal or workspace
// close, worktrunk installed too.
func TestFinishFromTheMainCheckout(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.has["wt"] = true
	fx.openPR()
	plan := fx.onMain(t, "# plan")
	fx.in.workspace = "ws:1"

	rec := &recorder{f: fx.f}

	if err := finish(t.Context(), rec, fx.in); err != nil {
		t.Fatal(err)
	}

	out := rec.buf.String()

	order := []string{
		ghView,
		ghMerge,
		ghView,
		lsRem,
		ghDel,
		dropRemoteRef,
		curBr,
		"git fetch origin main:main",
		"git merge-base --is-ancestor m3rge main",
		"git switch main",
		"git branch -D GH-1",
	}

	last := -1

	for _, step := range order {
		idx := indexAfter(fx.f.calls, step, last)
		if idx < 0 {
			t.Fatalf("%q did not run after step %d:\n%s", step, last, strings.Join(fx.f.calls, "\n"))
		}

		last = idx
	}

	for _, ran := range []string{
		"git pull", "git worktree remove", "wt remove", "herdr workspace close", "herdr workspace focus",
	} {
		if fx.f.called(ran) {
			t.Errorf("%q ran for a branch checked out in main", ran)
		}
	}

	if _, err := os.Stat(plan); !errors.Is(err, os.ErrNotExist) {
		t.Error("the plan was left in main's plans")
	}

	archived := filepath.Join(fx.main, ".f10", "plans", "archive", "GH-1.md")
	if body, err := os.ReadFile(archived); err != nil || string(body) != "# plan" {
		t.Errorf("archived plan = %q, %v", body, err)
	}

	if !strings.Contains(out, "GH-1 is checked out in the main checkout at "+fx.main+
		": after this report main switches to main") ||
		strings.Contains(out, "workspace:") || strings.Contains(out, "nothing to close") ||
		strings.Contains(out, "updated by fetch") {
		t.Errorf("report:\n%s", out)
	}
}

// A branch named by the project's own shape, the id past a prefix and no
// config link, is found by the id it carries: merged, its plan archived and
// the branch deleted, from the main checkout and from a worktree.
func TestFinishFindsTheIdPastAPrefix(t *testing.T) {
	const branch = "feat/GH-1-workdays"

	for _, inMain := range []bool{true, false} {
		t.Run(map[bool]string{true: "main checkout", false: "worktree"}[inMain], func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.openPR()
			fx.f.script(refsLocal, "")
			fx.f.script(allHeads, heads("main", branch))
			fx.f.script("git branch -D "+branch, "")

			// the host and origin are asked by the branch's name
			swap := strings.NewReplacer("GH-1 ", branch+" ", "heads/GH-1", "heads/"+branch,
				"origin GH-1", "origin "+branch, "origin/GH-1", "origin/"+branch)
			for _, key := range []string{ghView, ghMerge, ghStack, ghDel, lsRem, dropRemoteRef} {
				fx.f.answers[swap.Replace(key)] = fx.f.answers[key]
			}

			plan := fx.plan(t, "GH-1.md", "# plan")
			if inMain {
				plan = fx.onMain(t, "# plan")
				fx.f.script(worktrees, porcelain(branch, fx.main))
				fx.f.script(curBr, branch)
				fx.in.workspace = "ws:1"
			} else {
				fx.f.script(worktrees, porcelain(branch, fx.wt))
			}

			if _, err := fx.run(t); err != nil {
				t.Fatal(err)
			}

			if !fx.f.called("git branch -D " + branch) {
				t.Errorf("%s was not deleted:\n%s", branch, strings.Join(fx.f.calls, "\n"))
			}

			if _, err := os.Stat(plan); !errors.Is(err, os.ErrNotExist) {
				t.Error("the plan was not archived")
			}
		})
	}
}

// Main's own archive stays as it is: the current plan takes the next number.
func TestFinishFromTheMainCheckoutArchiveNumbering(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.onMain(t, "current")

	archive := filepath.Join(fx.main, ".f10", "plans", "archive")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"GH-1.md": "first", "GH-1.1.md": "older"} {
		if err := os.WriteFile(filepath.Join(archive, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"GH-1.md": "first", "GH-1.1.md": "older", "GH-1.2.md": "current"} {
		got, err := os.ReadFile(filepath.Join(archive, name))
		if err != nil || string(got) != body {
			t.Errorf("%s = %q, %v; want %q", name, got, err, body)
		}
	}
}

// A failure after the merge and before the report leaves main on the task's
// branch, so a rerun finds the same checkout, reads the PR as merged and
// completes.
func TestFinishFromTheMainCheckoutRerunsAfterAFailure(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	plan := fx.onMain(t, "# plan")
	fx.f.fail("git fetch origin main:main", shell.Result{Code: 1, Stderr: "fatal: refusing to fetch"})

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "refusing to fetch") {
		t.Fatalf("err = %v, want the fetch failure", err)
	}

	for _, ran := range []string{"git switch", "git branch -D"} {
		if fx.f.called(ran) {
			t.Errorf("%q ran although the fetch failed", ran)
		}
	}

	if _, err := os.Stat(plan); err != nil {
		t.Error("the plan was moved although the fetch failed")
	}

	fx.f.calls = nil
	fx.f.script(ghView, prMerged)
	fx.f.script(lsRem, "")
	fx.f.script("git fetch origin main:main", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if fx.f.called("gh pr merge") || !fx.f.called("git switch main") || !fx.f.called("git branch -D GH-1") {
		t.Errorf("rerun calls: %v", fx.f.calls)
	}

	if !strings.Contains(out, "was already merged") {
		t.Errorf("rerun report:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(fx.main, ".f10", "plans", "archive", "GH-1.md")); err != nil {
		t.Errorf("the rerun did not archive the plan: %v", err)
	}
}

// A switch or a delete git refuses at the end names the merge that landed
// and what is left.
func TestFinishFromTheMainCheckoutTailFails(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.onMain(t, "# plan")
	fx.f.fail("git switch main", shell.Result{Code: 1, Stderr: "error: cannot switch"})

	want := "https://gh/pr/9 merged, but switching the main checkout to main failed"
	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}

	if fx.f.called("git branch -D") {
		t.Error("the branch was deleted although the switch failed")
	}

	fx = newFinishFixture(t)
	fx.openPR()
	fx.onMain(t, "# plan")
	fx.f.fail("git branch -D GH-1", shell.Result{Code: 1, Stderr: "error: ref locked"})

	want = "https://gh/pr/9 merged and main is on main, but deleting GH-1 failed (git branch -D GH-1)"
	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}
}

// A dirty main checkout is refused before the host is reached.
func TestFinishFromTheMainCheckoutRefusesDirty(t *testing.T) {
	fx := newFinishFixture(t)
	fx.onMain(t, "# plan")
	fx.f.script("git status --porcelain", " M finish.go")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("err = %v", err)
	}

	if fx.f.called("gh ") || fx.f.called("git switch") {
		t.Errorf("the host or a switch was reached before the dirty check: %v", fx.f.calls)
	}
}

// prOpenBy is an open, mergeable PR that login opened.
func prOpenBy(login string) string {
	return `{"state":"OPEN","url":"https://gh/pr/9","number":9,"baseRefName":"main","mergeCommit":null,` +
		`"author":{"login":"` + login + `"}}`
}

const (
	ghUser   = "gh api user"
	unpushed = "git merge-base --is-ancestor refs/heads/GH-1 refs/remotes/origin/GH-1"
)

// Someone else's open PR, with neither flag, changes nothing and says which
// flag does what.
func TestFinishRefusesSomeoneElsesPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, prOpenBy("alice"))
	fx.f.script(ghUser, `{"login":"me"}`)

	_, err := fx.run(t)

	fp := foreignPullError{}
	if !errors.As(err, &fp) || fp.author != "alice" || !strings.Contains(err.Error(), "--merge") ||
		!strings.Contains(err.Error(), "--local") {
		t.Fatalf("err = %v, want the foreign PR refusal naming both flags", err)
	}

	if fx.f.called("gh pr merge") || fx.f.called("git worktree remove") || fx.f.called("herdr workspace close") {
		t.Errorf("finish changed something on a refusal: %v", fx.f.calls)
	}
}

// One's own PR merges as before, whatever case the host spells the login in.
func TestFinishMergesOwnPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.script(ghView, prOpenBy("Me"), prMerged)
	fx.f.script(ghUser, `{"login":"me"}`)

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.called(ghMerge) {
		t.Error("the user's own PR was not merged")
	}
}

// --merge merges someone else's PR without asking who the user is.
func TestFinishMergeFlagMergesSomeoneElsesPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.script(ghView, prOpenBy("alice"), prMerged)
	fx.in.merge = true

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.called(ghMerge) || fx.f.called(ghUser) {
		t.Errorf("--merge = %v, want a merge and no login read", fx.f.calls)
	}
}

// --local leaves the PR open: no merge, no remote delete, no pull, no
// dependents released, and the plan, worktree, branch and workspace go.
func TestFinishLocalLeavesThePROpen(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, prOpenBy("alice"))
	fx.f.script(unpushed, "")
	fx.plan(t, "GH-1.md", "# plan")
	fx.in.local = true

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	for _, banned := range []string{"gh pr merge", ghDel, lsRem, "git pull", cfgDeps, ghUser} {
		if fx.f.called(banned) {
			t.Errorf("--local ran %q: %v", banned, fx.f.calls)
		}
	}

	for _, want := range []string{"git worktree remove " + fx.wt, "git branch -D GH-1", "herdr workspace close ws:9"} {
		if !fx.f.called(want) {
			t.Errorf("--local did not run %q", want)
		}
	}

	for _, want := range []string{"pr:", "https://gh/pr/9", "is left open", "plan:"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "merge:") {
		t.Errorf("--local reported a merge:\n%s", out)
	}
}

// --local deletes the local branch, so commits origin lacks stop it first.
func TestFinishLocalRefusesUnpushedCommits(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, prOpenBy("alice"))
	fx.f.fail(unpushed, missing)
	fx.in.local = true

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "push or drop them") {
		t.Fatalf("err = %v, want the unpushed commits refused", err)
	}

	if fx.f.called("git worktree remove") || fx.f.called("git branch -D") {
		t.Errorf("the branch went although it had unpushed commits: %v", fx.f.calls)
	}
}

// A closed PR is refused without --local, and the refusal names it.
func TestFinishClosedPRNamesLocal(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, `{"state":"CLOSED","url":"https://gh/pr/9","number":9,"baseRefName":"main"}`)

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "--local") {
		t.Fatalf("err = %v, want the closed PR refused, naming --local", err)
	}
}

// noPull scripts a branch never pushed: the host has no request for it and
// origin no copy, and main is two commits behind nothing, so the merge is
// built in memory on main's head.
func (fx *finishFixture) noPull() {
	fx.f.fail(ghView, shell.Result{Code: 1, Stderr: `no pull requests found for branch "GH-1"`})
	fx.f.script(lsRem, "")
	fx.f.script("git config --get branch.GH-1.merge", "")
	fx.f.script("git rev-parse -q --verify refs/heads/main", "m0")
	fx.f.script("git rev-parse -q --verify refs/remotes/origin/main", "m0")
	fx.f.script("git rev-parse -q --verify refs/heads/GH-1", "t1")
	fx.f.script("git merge-tree --write-tree m0 refs/heads/GH-1", "tr33\n")
	fx.f.script("git rev-parse m0^{tree}", "old7ree")
	fx.f.script("git log --reverse --format=%s m0..refs/heads/GH-1", "first\nsecond")
}

// A branch never pushed, with no PR, is squashed into the local default
// branch by the declared method: the commit is built on main's head, main
// fast-forwards to it, dependents are released, and nothing is pushed,
// pulled or deleted on origin.
func TestFinishMergesABranchNeverPushedHere(t *testing.T) {
	for _, onDefault := range []bool{true, false} {
		t.Run(fmt.Sprintf("onDefault=%v", onDefault), func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.noPull()
			fx.in.method = "squash"
			fx.in.title = func(context.Context) (string, error) { return "Add the thing", nil }
			fx.f.script("git commit-tree tr33 -p m0 -m GH-1: Add the thing -m - first\n- second", "s9")
			fx.f.script("git merge --ff-only s9", "")
			fx.f.script("git update-ref refs/heads/main s9 m0", "")

			advance := "git merge --ff-only s9"
			if !onDefault {
				fx.f.script(curBr, "elsewhere")
				advance = "git update-ref refs/heads/main s9 m0"
			}

			out, err := fx.run(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			for _, want := range []string{"merge:", "s9", "merged into main here, nothing pushed", "no PR closes GH-1"} {
				if !strings.Contains(out, want) {
					t.Errorf("report lacks %q:\n%s", want, out)
				}
			}

			if strings.Contains(out, "pr:") {
				t.Errorf("the report names a PR:\n%s", out)
			}

			if !fx.f.called(advance) {
				t.Errorf("%q did not run: %q", advance, fx.f.calls)
			}

			for _, never := range []string{"git push", "git pull", "gh pr merge", ghDel, dropRemoteRef, "git fetch"} {
				if fx.f.called(never) {
					t.Errorf("%q ran on a local finish", never)
				}
			}

			for _, step := range []string{cfgDeps, "git worktree remove " + fx.wt, "git branch -D GH-1"} {
				if !fx.f.called(step) {
					t.Errorf("%q did not run", step)
				}
			}
		})
	}
}

// The merge and rebase methods land a branch never pushed too: a merge
// commit with both parents, a fast-forward to the branch's own head.
func TestFinishLandsANeverPushedBranchByMethod(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.in.method = "merge"
	fx.f.script("git commit-tree tr33 -p m0 -p t1 -m Merge branch 'GH-1'", "mm")
	fx.f.script("git merge --ff-only mm", "")

	if out, err := fx.run(t); err != nil || !strings.Contains(out, "mm") {
		t.Fatalf("merge: %v\n%s", err, out)
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.in.method = "rebase"
	fx.f.script("git merge-base --is-ancestor m0 refs/heads/GH-1", "")
	fx.f.script("git merge --ff-only t1", "")

	if out, err := fx.run(t); err != nil || !strings.Contains(out, "t1") {
		t.Fatalf("rebase: %v\n%s", err, out)
	}

	if fx.f.called("git commit-tree") {
		t.Error("a fast-forward wrote a commit")
	}
}

// A branch never pushed that conflicts with the default branch, or is
// behind it under the rebase method, is refused with nothing changed and
// the catchup named; one already in the default branch lands nothing.
func TestFinishRefusesANeverPushedBranchItCannotLand(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.f.fail("git merge-tree --write-tree m0 refs/heads/GH-1", shell.Result{Code: 1})

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "conflicts with main: run /f10:catchup") {
		t.Fatalf("conflict: err = %v", err)
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.in.method = "rebase"
	fx.f.fail("git merge-base --is-ancestor m0 refs/heads/GH-1", shell.Result{Code: 1})

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "lands only a fast-forward") {
		t.Fatalf("behind: err = %v", err)
	}

	for _, never := range []string{"git commit-tree", "git update-ref refs/heads/main", "git merge --ff-only", "git worktree remove"} {
		if fx.f.called(never) {
			t.Errorf("%q ran on a refusal", never)
		}
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.f.script("git rev-parse m0^{tree}", "tr33")

	out, err := fx.run(t)
	if err != nil || !strings.Contains(out, "GH-1 was already in main") || fx.f.called("git commit-tree") {
		t.Fatalf("rerun: %v\n%s", err, out)
	}
}

// A pushed branch with no PR stays a refusal, naming both ways out.
func TestFinishRefusesAPushedBranchWithoutAPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.fail(ghView, shell.Result{Code: 1, Stderr: `no pull requests found for branch "GH-1"`})

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "origin has GH-1 with no PR: open one, or delete origin's copy") {
		t.Fatalf("err = %v", err)
	}
}

// A local default branch that only lags origin's is not the merge's parent:
// the squash is built on origin's head, so origin's commits stay out of it,
// and main fast-forwards past them. Diverged is refused.
func TestFinishLandsANeverPushedBranchOnOriginsHead(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.in.method = "squash"
	fx.f.script("git rev-parse -q --verify refs/remotes/origin/main", "o1")
	fx.f.fail("git merge-base --is-ancestor o1 m0", shell.Result{Code: 1})
	fx.f.script("git merge-base --is-ancestor m0 o1", "")
	fx.f.script("git merge-tree --write-tree o1 refs/heads/GH-1", "tr33")
	fx.f.script("git rev-parse o1^{tree}", "o7ree")
	fx.f.script("git log --reverse --format=%s o1..refs/heads/GH-1", "only")
	fx.f.script("git commit-tree tr33 -p o1 -m only", "s9")
	fx.f.script("git merge --ff-only s9", "")

	if out, err := fx.run(t); err != nil || !strings.Contains(out, "s9") {
		t.Fatalf("behind: %v\n%s", err, out)
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.f.script("git rev-parse -q --verify refs/remotes/origin/main", "o1")
	fx.f.fail("git merge-base --is-ancestor o1 m0", shell.Result{Code: 1})
	fx.f.fail("git merge-base --is-ancestor m0 o1", shell.Result{Code: 1})

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "main and origin/main have diverged") {
		t.Fatalf("diverged: err = %v", err)
	}

	if fx.f.called("git commit-tree") || fx.f.called("git worktree remove") {
		t.Error("a diverged main was merged into")
	}
}

// --local merges nothing, so a branch with no PR stays the old refusal, and
// so does one tracking an upstream origin lacks, which may be a request the
// host merged and no longer lists by branch.
func TestFinishKeepsTheRefusalWhenNeverPushedIsNotProven(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.in.local = true

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "no pull requests found") {
		t.Fatalf("--local: err = %v", err)
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.f.script("git config --get branch.GH-1.merge", "refs/heads/GH-1")

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "tracks a branch origin no longer has") {
		t.Fatalf("upstream: err = %v", err)
	}

	if fx.f.called("git commit-tree") || fx.f.called("git merge-tree") {
		t.Error("a branch not proven unpushed was merged")
	}
}

// In-repo storage every checkout shares: the plan in main's plans dir is
// archived beside it, and the worktree holds nothing to move.
func TestFinishArchivesMainsSharedPlan(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()

	shared := filepath.Join(fx.main, ".f10", "plans")
	fx.in.plansDir = shared

	if err := os.MkdirAll(shared, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(shared, "GH-1.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if body, err := os.ReadFile(filepath.Join(shared, "archive", "GH-1.md")); err != nil || string(body) != "plan" {
		t.Errorf("archived plan = %q, %v", body, err)
	}
}

// A worktree from before storage moved to main's still holds its plan and
// the versions it archived: they move into the shared archive.
func TestFinishArchivesALegacyWorktreesPlan(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.plan(t, "GH-1.md", "current")
	fx.plan(t, filepath.Join("archive", "GH-1.1.md"), "older")

	shared := filepath.Join(fx.main, ".f10", "plans")
	fx.in.plansDir = shared

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"GH-1.md": "older", "GH-1.1.md": "current"} {
		got, err := os.ReadFile(filepath.Join(shared, "archive", name))
		if err != nil || string(got) != body {
			t.Errorf("%s = %q, %v; want %q", name, got, err, body)
		}
	}
}

// Finish records the branch's size against the base it forked from,
// lockfiles left out and the markdown share kept: a stacked branch against
// its base branch while it exists, a PR already merged with a merge commit
// from that commit's first parent, a branch never pushed against the local
// default branch. Nothing to measure records nothing.
func TestFinishRecordsTheBranchSize(t *testing.T) {
	const (
		numstat = "10\t2\tcli/stats.go\n5\t1\tdocs/cli.md\n40\t0\tgo.sum\n-\t-\tlogo.png"
		diff    = "git diff --numstat --no-renames "
	)

	sized := func(base string) *branchSize { return &branchSize{lines: 18, md: 6, files: 3, base: base} }

	for _, tc := range []struct {
		name  string
		setup func(fx *finishFixture)
		want  *branchSize
	}{
		{
			name:  "against the default branch",
			setup: func(fx *finishFixture) { fx.f.script(diff+"origin/main...refs/heads/GH-1", numstat) },
			want:  sized("origin/main"),
		},
		{
			name: "against its base branch",
			setup: func(fx *finishFixture) {
				fx.f.script("git config --get branch.GH-1."+cfgAfterBranch, "GH-0")
				fx.f.script(diff+"GH-0...refs/heads/GH-1", numstat)
			},
			want: sized("GH-0"),
		},
		{
			name: "a base branch already deleted falls back to the default branch",
			setup: func(fx *finishFixture) {
				fx.f.script("git config --get branch.GH-1."+cfgAfterBranch, "GH-0")
				fx.f.fail(diff+"GH-0...refs/heads/GH-1", shell.Result{Code: 128})
				fx.f.script(diff+"origin/main...refs/heads/GH-1", numstat)
			},
			want: sized("origin/main"),
		},
		{
			name: "a PR merged with a merge commit",
			setup: func(fx *finishFixture) {
				fx.f.script(diff+"origin/main...refs/heads/GH-1", "")
				fx.f.script("git rev-list --ancestry-path --merges --reverse refs/heads/GH-1..origin/main", "mc1\nmc2")
				fx.f.script(diff+"mc1^1...refs/heads/GH-1", numstat)
			},
			want: sized("mc1^1"),
		},
		{
			name: "a branch never pushed",
			setup: func(fx *finishFixture) {
				fx.noPull()
				fx.in.method = "merge"
				fx.f.script("git commit-tree tr33 -p m0 -p t1 -m Merge branch 'GH-1'", "mm")
				fx.f.script("git merge --ff-only mm", "")
				fx.f.script(diff+"main...refs/heads/GH-1", numstat)
			},
			want: sized("main"),
		},
		{
			name: "a branch never pushed, already landed",
			setup: func(fx *finishFixture) {
				fx.noPull()
				fx.f.script("git rev-parse m0^{tree}", "tr33")
			},
		},
		{
			name:  "no base resolves",
			setup: func(fx *finishFixture) { fx.f.fail(diff+"origin/main...refs/heads/GH-1", shell.Result{Code: 128}) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.openPR()
			tc.setup(fx)

			var got []branchSize

			prev := recordSize
			recordSize = func(_ context.Context, _, branch, task string, s branchSize) {
				if branch != "GH-1" || task != "GH-1" {
					t.Errorf("recorded for %s on %s", task, branch)
				}

				got = append(got, s)
			}

			t.Cleanup(func() { recordSize = prev })

			if out, err := fx.run(t); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			switch {
			case tc.want == nil && len(got) > 0:
				t.Errorf("recorded %+v, want nothing", got)
			case tc.want != nil && (len(got) != 1 || got[0] != *tc.want):
				t.Errorf("sizes %+v, want %+v", got, *tc.want)
			}
		})
	}
}

// The janitor runs once the task's own cleanup is done and before the
// workspace closes, since closing its own workspace ends the process.
func TestFinishSweepsBeforeTheWorkspaceCloses(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.script("sweep", "")
	fx.in.sweep = func(context.Context) { fx.f.answer("sweep") }

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	removed := slices.Index(fx.f.calls, "git branch -D GH-1")
	swept := slices.Index(fx.f.calls, "sweep")
	closed := slices.Index(fx.f.calls, "herdr workspace close ws:9")

	if removed < 0 || swept < removed || closed < swept {
		t.Errorf("branch removed at %d, swept at %d, workspace closed at %d", removed, swept, closed)
	}
}

// A bare finish is the branch's task, over the session's; a typed id still
// splits its suffix, and on the default branch, or under -C, nothing
// finishes unasked, the session's task included.
func TestFinishRef(t *testing.T) {
	repo := tracedRepo(t, "DM-5/fix", "")
	sessionTask(t, "DM-9")

	tg, err := targetAt(t.Context(), repo, false, "finish")
	if err != nil {
		t.Fatal(err)
	}

	if r, suffix, _, err := finishRef(t.Context(), tg, ""); err != nil || r.ID != "DM-5" || suffix != "" {
		t.Errorf("bare = %q %q %v, want DM-5 and no suffix", r.ID, suffix, err)
	}

	if _, _, branch, _ := finishRef(t.Context(), tg, ""); branch != "DM-5/fix" {
		t.Errorf("bare branch = %q, want the one checked out here", branch)
	}

	if r, suffix, _, err := finishRef(t.Context(), tg, "DM-7-b"); err != nil || r.ID != "DM-7" || suffix != "b" {
		t.Errorf("DM-7-b = %q %q %v, want DM-7 and suffix b", r.ID, suffix, err)
	}

	scoped, err := targetAt(t.Context(), repo, true, "finish")
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = finishRef(t.Context(), scoped, "")
	if err == nil || !strings.Contains(err.Error(), "explicit task id") {
		t.Errorf("bare under -C: err = %v, want a refusal", err)
	}

	onMain := tracedRepo(t, "main", "")
	sessionTask(t, "DM-9")

	tg, err = targetAt(t.Context(), onMain, false, "finish")
	if err != nil {
		t.Fatal(err)
	}

	r, _, _, err := finishRef(t.Context(), tg, "")
	if err == nil || !strings.Contains(err.Error(), "branch names no task") {
		t.Errorf("bare on main with a session task = %q, %v; want a refusal", r.ID, err)
	}
}

// A bare call in a suffixed task's worktree finishes that branch, not the
// unsuffixed sibling the id alone would find.
func TestFinishTargetTakesTheBareCallsBranch(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(refsLocal, heads("GH-1", "GH-1-b"))
	fx.f.script(worktrees, porcelain("GH-1", fx.main+".other", "GH-1-b", fx.wt))
	fx.in.branch = "GH-1-b"

	name, path, _, err := finishTarget(t.Context(), fx.in)
	if err != nil || name != "GH-1-b" || path != fx.wt {
		t.Errorf("finishTarget = %q %q %v, want GH-1-b at %s", name, path, err, fx.wt)
	}
}

// sessionTask records id as this session's task, as a plan run's badge does.
func sessionTask(t *testing.T, id string) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")

	if err := (&state.Run{Session: "sess-1", Task: id}).Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// finish names each action as it starts, for drive's record: the merge by
// its PR, or the branch it lands here, then the local cleanup.
func TestFinishReportsProgress(t *testing.T) {
	t.Run("pr", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()

		var got []string

		fx.in.progress = func(a string) { got = append(got, a) }

		if out, err := fx.run(t); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		// removing the worktree deletes the branch and the record: nothing after it
		want := []string{"merging #9", "pulling main", "archiving the plan", "removing worktree"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("progress = %q, want %q", got, want)
		}
	})

	t.Run("local", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.noPull()
		fx.in.method = "merge"
		fx.f.script("git commit-tree tr33 -p m0 -p t1 -m Merge branch 'GH-1'", "mm")
		fx.f.script("git merge --ff-only mm", "")

		var got []string

		fx.in.progress = func(a string) { got = append(got, a) }

		if out, err := fx.run(t); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		if len(got) == 0 || got[0] != "merging GH-1 into main" || slices.Contains(got, "pulling main") {
			t.Errorf("progress = %q, want a local merge first and no pull", got)
		}
	})
}
