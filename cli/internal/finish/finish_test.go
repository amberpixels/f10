package finish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The fixture every finish test starts from: GH-1's branch is checked out
// at <tmp>/repo.GH-1 beside the main checkout at <tmp>/repo, both real
// directories so the plan move and the cwd check run against disk.
type finishFixture struct {
	f    *testkit.Fakes
	main string
	wt   string
	in   FinishInput
}

// dependent records GH-2 as started --after GH-1, and reports whether
// finish released it: its dependency swapped for the landed mark.
func (fx *finishFixture) dependent() func() bool {
	fx.f.Meta.Seed("GH-2", branchmeta.After, "GH-1")
	fx.f.Meta.Seed("GH-2", branchmeta.AfterBranch, "GH-1")

	return func() bool { return fx.f.Meta.Wrote("GH-2", branchmeta.Landed, "GH-1") }
}

func newFinishFixture(t *testing.T) *finishFixture {
	t.Helper()

	root := t.TempDir()
	fx := &finishFixture{
		f:    testkit.NewFakes(t),
		main: filepath.Join(root, "repo"),
		wt:   filepath.Join(root, "repo.GH-1"),
	}

	for _, d := range []string{fx.main, fx.wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	fx.in = FinishInput{
		Main:    fx.main,
		Storage: storage.InRepo(fx.main),
		Task:    testkit.Gh1,
		Host:    hostops.NewHost("gh", fx.main),
		Cwd:     fx.main,
	}

	f := fx.f
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", fx.wt))
	f.Script("git status --porcelain", "")
	f.Script(testkit.WsList, fx.workspaces(true))
	f.Script(testkit.HeadRef, "origin/main")
	f.Script(testkit.CurBr, "main")
	f.Script("git pull --ff-only", "")
	f.Script("git merge-base --is-ancestor m3rge main", "")

	f.Script(testkit.LsRem, "abc\trefs/heads/GH-1")
	f.Script(testkit.GhStack, "[]")
	f.Script(testkit.GhDel, "")
	f.Script(testkit.DropRemoteRef, "")
	f.Script("herdr workspace close ws:9", "{}")
	f.Script("git worktree remove "+fx.wt, "")
	f.Script("git branch -D GH-1", "")

	return fx
}

func (fx *finishFixture) workspaces(open bool) string {
	main := `{"workspace_id":"ws:1","label":"repo","focused":true,"worktree":{"checkout_path":"` + fx.main + `"}}`
	if !open {
		return `{"result":{"workspaces":[` + main + `]}}`
	}

	task := `{"workspace_id":"ws:9","label":"GH-1","agent_status":"idle","worktree":{"checkout_path":"` + fx.wt + `"}}`

	return `{"result":{"workspaces":[` + main + `,` + task + `]}}`
}

// plan writes GH-1's plan in the shared storage root.
func (fx *finishFixture) plan(t *testing.T, body string) string {
	t.Helper()

	p := fx.in.Storage.Plan("GH-1")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	return p
}

func (fx *finishFixture) openPR() {
	fx.f.Script(testkit.GhView, testkit.PrOpen, testkit.PrMerged)
	fx.f.Script(testkit.GhRepo, testkit.AllowAll)
	fx.f.Script(testkit.GhMerge, "")
}

func (fx *finishFixture) run(t *testing.T) (string, error) {
	t.Helper()

	var out bytes.Buffer

	err := Finish(t.Context(), &out, fx.in)

	return out.String(), err
}

// With traces on, the squash body is the PR description plus the task's
// trailer, so the commit on the default branch still names the task; the
// state block says finish before the merge and never reaches the commit.
func TestFinishSquashBodyKeepsTheTask(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Traces = true
	fx.in.Method = "squash"
	fx.plan(t, "# plan")

	open := `{"state":"OPEN","url":"https://gh/pr/9","number":9,"baseRefName":"main","body":"stale"}`
	current := `{"body":"Adds the thing.\n\nCloses #1\n\n<!-- state {\"v\":1,\"task\":\"GH-1\",\"step\":\"judge\",\"round\":1} -->\n"}`
	merge := "gh pr merge GH-1 --squash --body Adds the thing.\n\nCloses #1\n\nTask: GH-1\n"
	fx.f.Script(testkit.GhView, open, testkit.PrMerged)
	fx.f.Script("gh pr view GH-1 --json body", current)
	fx.f.Absent = append(fx.f.Absent, "gh pr edit GH-1 --body-file ")
	fx.f.Script(merge, "")
	edited := captureBodyEdits(t)

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called(merge) {
		t.Errorf("the squash carried no body:\n%s", strings.Join(fx.f.Calls, "\n"))
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
	seam.Swap(t, &shell.Capture, func(ctx context.Context, dir, name string, args ...string) (shell.Result, error) {
		if name == "gh" && len(args) > 2 && args[0] == "pr" && args[1] == "edit" {
			data, err := os.ReadFile(args[len(args)-1])
			if err != nil {
				t.Fatal(err)
			}

			last = string(data)
		}

		return prev(ctx, dir, name, args...)
	})

	return &last
}

// A body the host will not hand over or take back is a note on the report;
// the merge still runs, with the description finish read first.
func TestFinishStateBlockFailureIsANote(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Traces = true
	fx.in.Method = "squash"
	fx.plan(t, "# plan")

	open := `{"state":"OPEN","url":"https://gh/pr/9","number":9,"baseRefName":"main","body":"Adds the thing.\n"}`
	merge := "gh pr merge GH-1 --squash --body Adds the thing.\n\nTask: GH-1\n"
	fx.f.Script(testkit.GhView, open, testkit.PrMerged)
	fx.f.Answers["gh pr view GH-1 --json body"] = []shell.Result{{Code: 1, Stderr: "rate limited"}}
	fx.f.Script(merge, "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called(merge) || !strings.Contains(out, "state block was not rewritten: gh: rate limited") {
		t.Errorf("merge called %v, report:\n%s", fx.f.Called(merge), out)
	}
}

// A bundle's body was just rewritten to keep a rider open, so a body the
// host will not hand back stops the finish: the stale one would close it.
func TestFinishStateBlockFailureStopsABundle(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.Traces = true
	fx.in.Method = "squash"
	fx.in.Task, fx.in.Close, fx.in.HostCloses = testkit.Bundle12, []string{}, true
	fx.f.Answers["gh pr view GH-1 --json body"] = []shell.Result{
		{Stdout: `{"body":"Closes #1\nCloses #2"}`},
		{Code: 1, Stderr: "rate limited"},
	}
	fx.f.Absent = append(fx.f.Absent, "gh pr edit GH-1 --body-file ")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err = %v, want the host's words", err)
	}

	if fx.f.Called("gh pr merge") {
		t.Errorf("merged on a stale body:\n%s", strings.Join(fx.f.Calls, "\n"))
	}
}

func TestMergeBody(t *testing.T) {
	bundle := FinishInput{Traces: true, Task: ref.Ref{ID: "GH-1", Bundle: []string{"GH-1", "GH-2"}}}
	described := "  Adds the thing.\n"

	cases := []struct {
		name        string
		in          FinishInput
		description string
		method      string
		want        string
	}{
		{"off", FinishInput{Task: testkit.Gh1}, described, "--squash", ""},
		{"rebase", FinishInput{Traces: true, Task: testkit.Gh1}, described, "--rebase", ""},
		{"glab plain merge", FinishInput{Traces: true, Task: testkit.Gh1}, described, "", ""},
		{
			"squash",
			FinishInput{Traces: true, Task: testkit.Gh1},
			described,
			"--squash",
			"Adds the thing.\n\nTask: GH-1\n",
		},
		{
			"merge commit",
			FinishInput{Traces: true, Task: testkit.Gh1},
			described,
			"--merge",
			"Adds the thing.\n\nTask: GH-1\n",
		},
		{"empty description", FinishInput{Traces: true, Task: testkit.Gh1}, "", "--squash", "Task: GH-1\n"},
		{
			"state block stripped",
			FinishInput{Traces: true, Task: testkit.Gh1},
			"## Summary\n\n<!-- state {\"v\":1,\"step\":\"finish\"} -->\n\nAdds the thing.\n", "--squash",
			"## Summary\n\nAdds the thing.\n\nTask: GH-1\n",
		},
		{"bundle", bundle, "", "--squash", "Task: GH-1\nTask: GH-2\n"},
		{
			"a follow-up series",
			FinishInput{Traces: true, Task: testkit.Gh1, Change: "GH-1.2"},
			"",
			"--squash",
			"Task: GH-1\nChange: GH-1.2\n",
		},
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
	fx.in.Traces = true
	fx.in.Method = "squash"
	fx.in.Task, fx.in.Close, fx.in.HostCloses = testkit.Bundle12, []string{}, true
	fx.f.Script("gh pr view GH-1 --json body", `{"body":"Closes #1\nCloses #2"}`, `{"body":"Closes #1\nPart of #2"}`)
	fx.f.Absent = append(fx.f.Absent, "gh pr edit GH-1 --body-file ")

	merge := "gh pr merge GH-1 --squash --body Closes #1\nPart of #2\n\nTask: GH-1\nTask: GH-2\n"
	fx.f.Script(merge, "")

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called(merge) {
		t.Errorf("the squash body kept a stale Closes:\n%s", strings.Join(fx.f.Calls, "\n"))
	}
}

// The happy path from main's checkout.
func TestFinishMergesAndCleansUp(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	plan := fx.plan(t, "# plan")
	testkit.WriteFile(t, filepath.Join(fx.main, ".f10", "reviews", "GH-1", "1.md"), "task: GH-1\nround: 1\n")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	archived := filepath.Join(fx.main, ".f10", "plans", "archive", "GH-1.md")

	// the task's next PR starts its own series of review rounds
	reviews := filepath.Join(fx.main, ".f10", "reviews", "archive", "GH-1.1")
	if !fsx.Exists(filepath.Join(reviews, "1.md")) || !strings.Contains(out, reviews) {
		t.Errorf("findings not archived to %s:\n%s", reviews, out)
	}

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
		testkit.GhView,
		testkit.GhMerge,
		testkit.GhView,
		testkit.LsRem,
		testkit.GhDel,
		testkit.DropRemoteRef,
		testkit.CurBr,
		"git pull --ff-only",
		"git merge-base --is-ancestor m3rge main",
		"git worktree remove " + fx.wt,
		"git branch -D GH-1",
		"herdr workspace close ws:9",
	}

	last := -1

	for _, step := range order {
		idx := testkit.IndexAfter(fx.f.Calls, step, last)
		if idx < 0 {
			t.Fatalf("%q did not run after step %d:\n%s", step, last, strings.Join(fx.f.Calls, "\n"))
		}

		last = idx
	}

	if fx.f.Called("herdr workspace focus") {
		t.Error("main's workspace was focused although the command ran from it")
	}

	if strings.Contains(strings.Join(fx.f.Calls, "\n"), "--admin") {
		t.Error("the merge used --admin")
	}

	// the repo allows every method and project.md declares none, so the
	// squash default is said rather than taken in silence
	if !strings.Contains(out, "merged with --squash: project.md declares no merge method") {
		t.Errorf("the default merge method was not noted:\n%s", out)
	}
}

// A branch started with --after on this one loses its dependency once this
// one merged, and is marked landed so its next ship or catchup brings main
// in; a dependency on some other branch is left alone.
func TestFinishReleasesDependents(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Meta.Seed("GH-8/slug", branchmeta.AfterBranch, "GH-1")
	fx.f.Meta.Seed("GH-9", branchmeta.AfterBranch, "GH-2")
	fx.f.Meta.Seed("GH-8/slug", branchmeta.After, "GH-1")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"unset branch.GH-8/slug.f10-after",
		"unset branch.GH-8/slug.f10-after-branch",
		"set branch.GH-8/slug.f10-landed GH-1",
	} {
		if !fx.f.Called(want) {
			t.Errorf("%q did not run: %v", want, fx.f.Calls)
		}
	}

	if fx.f.Called("unset branch.GH-9") || fx.f.Called("set branch.GH-9") {
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
	fx.plan(t, "# plan")

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
		if fx.f.Called(ran) {
			t.Errorf("%q ran although the plan was not archived", ran)
		}
	}
}

// An already merged PR is confirmed, not merged again, and the remote
// branch is deleted only when origin still has it.
func TestFinishAlreadyMerged(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.f.Script(testkit.LsRem, "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if fx.f.Called("gh pr merge") || fx.f.Called(testkit.GhDel) {
		t.Errorf("merged or deleted again: %v", fx.f.Calls)
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
	fx.f.Script(testkit.GhView, testkit.PrOpen)
	fx.f.Script(testkit.GhRepo, testkit.AllowAll)
	fx.f.Fail(
		testkit.GhMerge,
		shell.Result{
			Code:   1,
			Stderr: "X Pull request #9 is not mergeable: the base branch policy prohibits the merge.",
		},
	)
	fx.plan(t, "# plan")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "base branch policy prohibits the merge") {
		t.Fatalf("err = %v, want the host's reason", err)
	}

	for _, p := range []string{"git pull", "git fetch", "herdr workspace close", "git worktree remove", "git branch -D", testkit.GhDel} {
		if fx.f.Called(p) {
			t.Errorf("%q ran after the host refused", p)
		}
	}

	if _, err := os.Stat(fx.in.Storage.Plan("GH-1")); err != nil {
		t.Error("the plan was moved although the merge failed")
	}
}

func TestFinishRefusesClosedPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, `{"state":"CLOSED","url":"https://gh/pr/9","baseRefName":"main"}`)

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "closed without a merge") {
		t.Fatalf("err = %v, want a refusal on the closed PR", err)
	}

	if fx.f.Called("gh pr merge") {
		t.Error("a closed PR was merged")
	}
}

// A PR stacked on another task's branch waits for that task.
func TestFinishRefusesStackedPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, `{"state":"OPEN","url":"https://gh/pr/9","baseRefName":"GH-7/base"}`)

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "stacked on GH-7/base") {
		t.Fatalf("err = %v, want the stacked refusal", err)
	}

	if fx.f.Called("gh pr merge") || fx.f.Called(testkit.GhRepo) {
		t.Error("the stacked PR was merged")
	}
}

// A PR stacked on the branch moves to main before the branch goes: a
// refs-API delete closes every PR still based on it.
func TestFinishRetargetsStackedPRsBeforeDelete(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Script(testkit.GhStack, `[{"number":12,"url":"https://gh/pr/12","headRefName":"GH-2/next"}]`)
	fx.f.Script("gh pr edit 12 --base main", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	edit, del := slices.Index(fx.f.Calls, "gh pr edit 12 --base main"), slices.Index(fx.f.Calls, testkit.GhDel)
	if edit < 0 || del < 0 || edit > del {
		t.Errorf("want the retarget before the delete:\n%s", strings.Join(fx.f.Calls, "\n"))
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
	fx.f.Script(testkit.GhStack, `[{"number":12,"url":"https://gh/pr/12","headRefName":"GH-2/next"}]`)
	fx.f.Fail("gh pr edit 12 --base main", shell.Result{Code: 1, Stderr: "GraphQL: Resource not accessible"})
	fx.plan(t, "# plan")

	_, err := fx.run(t)
	if err == nil {
		t.Fatal("finish went on after a failed retarget")
	}

	for _, want := range []string{"https://gh/pr/12", "Resource not accessible", "origin/GH-1 is left in place"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to carry %q", err, want)
		}
	}

	for _, p := range []string{testkit.GhDel, testkit.DropRemoteRef, "git worktree remove", "git branch -D"} {
		if fx.f.Called(p) {
			t.Errorf("%q ran after the retarget failed", p)
		}
	}

	if _, err := os.Stat(fx.in.Storage.Plan("GH-1")); err != nil {
		t.Error("the plan was moved although the retarget failed")
	}
}

// A failed retarget after a successful one names the one already moved,
// since the error is all the run prints.
func TestFinishNamesRetargetsBeforeAFailure(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Script(testkit.GhStack, `[{"number":12,"url":"https://gh/pr/12"},{"number":13,"url":"https://gh/pr/13"}]`)
	fx.f.Script("gh pr edit 12 --base main", "")
	fx.f.Fail("gh pr edit 13 --base main", shell.Result{Code: 1, Stderr: "GraphQL: Resource not accessible"})

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "already retargeted: https://gh/pr/12") {
		t.Fatalf("err = %v, want it to name the PR already moved", err)
	}

	if fx.f.Called(testkit.GhDel) {
		t.Error("the branch was deleted after a failed retarget")
	}
}

// A listing the host refuses keeps the branch too: an unlisted stacked PR
// would be closed by the delete.
func TestFinishKeepsBranchWhenListingFails(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Fail(testkit.GhStack, shell.Result{Code: 1, Stderr: "HTTP 502"})

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "listing the requests based on GH-1 failed") ||
		!strings.Contains(err.Error(), "origin/GH-1 is left in place") {
		t.Fatalf("err = %v, want the listing failure with the branch kept", err)
	}

	for _, p := range []string{testkit.GhDel, testkit.DropRemoteRef, "git worktree remove", "git branch -D"} {
		if fx.f.Called(p) {
			t.Errorf("%q ran after the listing failed", p)
		}
	}
}

// A rerun on a PR already merged retargets what the branch still holds.
func TestFinishRetargetsAfterEarlierMerge(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.f.Script(testkit.GhStack, `[{"number":12,"url":"https://gh/pr/12","headRefName":"GH-2/next"}]`)
	fx.f.Script("gh pr edit 12 --base main", "")

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if fx.f.Called("gh pr merge") || !fx.f.Called("gh pr edit 12 --base main") || !fx.f.Called(testkit.GhDel) {
		t.Errorf("calls:\n%s", strings.Join(fx.f.Calls, "\n"))
	}
}

// A pull that fails after the merge is a note: the rest of the cleanup
// runs, the main-pull lease is released, and the deferred pull is recorded
// for a retry that clears it once the pull goes through.
func TestFinishDefersAFailedPull(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Root = t.TempDir()
	fx.openPR()
	fx.plan(t, "# plan")
	fx.f.Fail("git pull --ff-only", shell.Result{Code: 1, Stderr: "fatal: Not possible to fast-forward, aborting."})

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("finish = %v, want the pull deferred", err)
	}

	for _, p := range []string{"git worktree remove " + fx.wt, "git branch -D GH-1", "herdr workspace close ws:9"} {
		if !fx.f.Called(p) {
			t.Errorf("%q did not run after the pull failed", p)
		}
	}

	if !strings.Contains(out, "main not pulled: the local main has commits origin/main lacks") ||
		!strings.Contains(out, "git -C "+fx.main+" switch main && git -C "+fx.main+" rebase origin/main") {
		t.Errorf("report:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(fx.main, ".f10", "plans", "archive", "GH-1.md")); err != nil {
		t.Errorf("the plan was not archived: %v", err)
	}

	if _, ok := drivestore.Probe(fx.in.Root, mainPullKey); ok {
		t.Error("the main-pull lease outlived the failed pull")
	}

	if def, sha := ReadPendingPull(fx.in.Root); def != "main" || sha != "m3rge" {
		t.Fatalf("pending pull = %q %q, want main m3rge", def, sha)
	}

	fx.f.Calls = nil
	fx.f.Script("git pull --ff-only", "")

	line, pending := RetryPendingPull(t.Context(), fx.in.Root, fx.main)
	if pending || !strings.Contains(line, "pulled main") {
		t.Errorf("retry = %q, %v, want it pulled", line, pending)
	}

	if def, _ := ReadPendingPull(fx.in.Root); def != "" {
		t.Error("the pending pull outlived its retry")
	}

	if line, pending := RetryPendingPull(t.Context(), fx.in.Root, fx.main); line != "" || pending {
		t.Errorf("retry with nothing pending = %q, %v", line, pending)
	}
}

// A lock that outlasts the pull's own waits, or another finish's lease,
// names the lock; anything else carries git's words.
func TestDeferredPullNote(t *testing.T) {
	lock := &gitx.ExitError{
		Args:   []string{"pull"},
		Code:   1,
		Stderr: "error: cannot lock ref 'refs/remotes/origin/main'",
	}

	for _, c := range []struct {
		err  error
		want string
	}{
		{lock, "main not pulled: another git held origin's refs; f10 retries the pull"},
		{fmt.Errorf("%w: lease", errPullBusy), "another git held origin's refs"},
		{errors.New("boom"), "main not pulled: boom; settle it with git -C /m pull --ff-only; f10 retries the pull"},
		{
			&gitx.ExitError{Args: []string{"pull"}, Code: 1, Stderr: "error: 'refs/remotes/origin/foo' exists; cannot create"},
			"a stale origin ref is in the way; settle it with git -C /m remote prune origin",
		},
	} {
		if got := deferredPullNote(c.err, "/m", "main"); !strings.Contains(got, c.want) {
			t.Errorf("deferredPullNote(%v) = %q, want %q in it", c.err, got, c.want)
		}
	}
}

// A plan that cannot be archived keeps the worktree and branch, since a
// rerun finds the task by them, and the rerun completes.
func TestFinishKeepsTheWorktreeWhenThePlanIsNotArchived(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.plan(t, "# plan")

	blocker := filepath.Join(fx.main, ".f10", "plans", "archive")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "archiving the plan failed") ||
		!strings.Contains(err.Error(), "rerun f10 finish GH-1") {
		t.Fatalf("err = %v, want the archive named and a rerun", err)
	}

	for _, p := range []string{"git worktree remove", "git branch -D", "herdr workspace close"} {
		if fx.f.Called(p) {
			t.Errorf("%q ran although the plan is not archived", p)
		}
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}

	fx.f.Calls = nil
	fx.f.Script(testkit.GhView, testkit.PrMerged)

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if fx.f.Called("gh pr merge") || !fx.f.Called("git worktree remove "+fx.wt) || !fx.f.Called("git branch -D GH-1") {
		t.Errorf("rerun calls:\n%s", strings.Join(fx.f.Calls, "\n"))
	}
}

// A workspace that will not close does not undo the rest: the worktree is
// gone and the error names the close.
func TestFinishRunsOnPastAFailedClose(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Fail("herdr workspace close ws:9", shell.Result{Code: 1, Stderr: "no such workspace"})

	// past the branch's removal a rerun would read the task as finished, so
	// the error says to settle it by hand
	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "closing workspace ws:9 failed") ||
		!strings.Contains(err.Error(), "settle what failed by hand") || strings.Contains(err.Error(), "rerun") {
		t.Fatalf("err = %v", err)
	}

	if !fx.f.Called("git worktree remove "+fx.wt) || !fx.f.Called("git branch -D GH-1") {
		t.Errorf("calls:\n%s", strings.Join(fx.f.Calls, "\n"))
	}
}

// On GitLab a source branch the merge left behind has its MRs retargeted
// by iid before the API deletes it.
func TestFinishRetargetsStackedMRsOnGitLab(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Host = hostops.NewHost("glab", fx.main)
	fx.f.Script(
		"glab mr view GH-1 -F json",
		`{"state":"merged","web_url":"https://gl/mr/4","iid":4,"target_branch":"main","squash_commit_sha":"m3rge"}`,
	)
	fx.f.Script(
		"glab mr list --target-branch GH-1 --per-page 100 -F json",
		`[{"iid":7,"web_url":"https://gl/mr/7","source_branch":"GH-2/next"}]`,
	)
	fx.f.Script("glab mr update 7 --target-branch main", "")
	fx.f.Script("glab api -X DELETE projects/:fullpath/repository/branches/GH-1", "")

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	edit := slices.Index(fx.f.Calls, "glab mr update 7 --target-branch main")
	del := slices.Index(fx.f.Calls, "glab api -X DELETE projects/:fullpath/repository/branches/GH-1")

	if edit < 0 || del < 0 || edit > del {
		t.Errorf("want the retarget before the delete:\n%s", strings.Join(fx.f.Calls, "\n"))
	}
}

// A dirty worktree is named file by file, before the host is reached.
func TestFinishRefusesDirtyWorktree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script("git status --porcelain", " M cli/cmd/f10/finish.go\n?? notes.txt")

	_, err := fx.run(t)
	if err == nil {
		t.Fatal("a dirty worktree was finished")
	}

	for _, want := range []string{"uncommitted changes", " M cli/cmd/f10/finish.go", "?? notes.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to carry %q", err, want)
		}
	}

	if fx.f.Called("gh ") || fx.f.Called("herdr ") {
		t.Errorf("the host or herdr was reached before the dirty check: %v", fx.f.Calls)
	}
}

// From inside the worktree the command refuses without --yes; with it, the
// report precedes the focus on main's workspace and the close.
func TestFinishSelfClose(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Cwd = filepath.Join(fx.wt, "cli")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "would close its own workspace (GH-1)") {
		t.Fatalf("err = %v, want the self-close refusal", err)
	}

	if fx.f.Called("gh ") {
		t.Error("the host was reached before the self-close refusal")
	}

	fx = newFinishFixture(t)
	fx.in.Cwd = filepath.Join(fx.wt, "cli")
	fx.in.Flags.Yes = true
	fx.openPR()
	fx.f.Script("herdr workspace focus ws:1", "{}")

	// a writer that records which herdr calls had run when the report landed
	rec := &recorder{f: fx.f}

	if err := Finish(t.Context(), rec, fx.in); err != nil {
		t.Fatal(err)
	}

	if rec.sawFocus || rec.sawClose {
		t.Error("the workspace was focused or closed before the report was printed")
	}

	focus, closeIdx := testkit.IndexAfter(
		fx.f.Calls,
		"herdr workspace focus ws:1",
		-1,
	), testkit.IndexAfter(
		fx.f.Calls,
		"herdr workspace close ws:9",
		-1,
	)
	if focus < 0 || closeIdx < 0 || focus > closeIdx {
		t.Errorf("focus then close expected, calls: %v", fx.f.Calls)
	}

	if !strings.Contains(rec.buf.String(), "this workspace closes after this report") {
		t.Errorf("the self-close note is missing:\n%s", rec.buf.String())
	}
}

// recorder notes, at the moment the report is written, whether the
// workspace calls had already run.
type recorder struct {
	f        *testkit.Fakes
	buf      bytes.Buffer
	sawFocus bool
	sawClose bool
}

func (r *recorder) Write(p []byte) (int, error) {
	r.sawFocus = r.sawFocus || r.f.Called("herdr workspace focus")
	r.sawClose = r.sawClose || r.f.Called("herdr workspace close")

	return r.buf.Write(p)
}

// HERDR_WORKSPACE_ID naming the task's workspace is the self-close case
// too, wherever the shell has cd'd to.
func TestFinishSelfCloseByWorkspaceID(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Workspace = "ws:9"

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "would close its own workspace") {
		t.Fatalf("err = %v, want the self-close refusal", err)
	}
}

// With no workspace on the worktree there is nothing to close, and the
// report says so.
func TestFinishWithoutAWorkspace(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.WsList, fx.workspaces(false))
	fx.openPR()

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if fx.f.Called("herdr workspace close") {
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
	fx.f.Script(testkit.CurBr, "spike/thing")
	fx.f.Script("git fetch origin main:main", "")
	fx.openPR()

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if fx.f.Called("git pull") || !fx.f.Called("git fetch origin main:main") {
		t.Errorf("calls: %v", fx.f.Calls)
	}

	if !strings.Contains(out, "main updated by fetch") {
		t.Errorf("report:\n%s", out)
	}
}

// A merge the local default branch still lacks after a pull is a deferred
// pull: the cleanup runs on, since the host holds the merge.
func TestFinishDefersWhenMergeIsNotInMain(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Fail("git merge-base --is-ancestor m3rge main", shell.Result{Code: 1})
	fx.openPR()
	fx.plan(t, "# plan")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "merge commit m3rge is not in main after pulling") {
		t.Errorf("report:\n%s", out)
	}

	for _, p := range []string{"herdr workspace close", "git worktree remove", "git branch -D"} {
		if !fx.f.Called(p) {
			t.Errorf("%q did not run", p)
		}
	}
}

// Archive numbering: an archived GH-1.md already in main makes this one
// GH-1.1.md, and the worktree's own archived versions move along first.
func TestFinishArchiveNumbering(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.plan(t, "current")

	archived := filepath.Join(fx.main, ".f10", "plans", "archive")
	if err := os.MkdirAll(archived, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"GH-1.md": "first", "GH-1.1.md": "older"} {
		if err := os.WriteFile(filepath.Join(archived, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"GH-1.md": "first", "GH-1.1.md": "older", "GH-1.2.md": "current"} {
		got, err := os.ReadFile(filepath.Join(archived, name))
		if err != nil || string(got) != body {
			t.Errorf("%s = %q, %v; want %q", name, got, err, body)
		}
	}

	if !strings.Contains(out, filepath.Join(archived, "GH-1.2.md")) {
		t.Errorf("report does not name the archived plan:\n%s", out)
	}
}

// Out-of-tree storage: the plan moves within the shared root.
func TestFinishArchivesOutOfTree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()

	root := t.TempDir()
	shared := filepath.Join(root, "plans")
	fx.in.Storage = storage.Root(root)

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

	if note := removeImages(storage.InRepo(main), "GH-1"); note != "" {
		t.Errorf("removeImages = %q, want no note", note)
	}

	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("main's images survived: %v", err)
	}
}

// With worktrunk on PATH the removal goes through wt and deletes the branch.
func TestFinishRemovesThroughWorktrunk(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Has["wt"] = true
	fx.f.Script("wt remove --yes --force-delete --foreground --format json GH-1", "{}")
	fx.openPR()

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if fx.f.Called("git worktree remove") || fx.f.Called("git branch -D") {
		t.Error("git removed the worktree although wt is installed")
	}
}

// The GitLab path: glab's spellings, squash from the project's option,
// auto-merge off, the source branch removed by the merge itself.
func TestFinishOnGitLab(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Host = hostops.NewHost("glab", fx.main)
	fx.f.Script(
		"glab mr view GH-1 -F json",
		`{"state":"opened","web_url":"https://gl/mr/4","iid":4,"target_branch":"main"}`,
		`{"state":"merged","web_url":"https://gl/mr/4","iid":4,"target_branch":"main","squash_commit_sha":"m3rge","merge_commit_sha":"other"}`,
	)
	fx.f.Script("glab repo view -F json", `{"squash_option":"default_on"}`)
	fx.f.Script("glab mr merge GH-1 --remove-source-branch --auto-merge=false --yes --squash", "")
	fx.f.Script(testkit.LsRem, "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called("glab mr merge GH-1 --remove-source-branch --auto-merge=false --yes --squash") {
		t.Errorf("calls: %v", fx.f.Calls)
	}

	if fx.f.Called("glab api") {
		t.Error("the remote branch was deleted although the merge removed it")
	}

	if !strings.Contains(out, "↗ https://gl/mr/4") || !strings.Contains(out, "m3rge") {
		t.Errorf("report:\n%s", out)
	}
}

func TestFinishRefusesWithoutBranchOrWorktree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.RefsLocal, "")

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "has no branch here") {
		t.Errorf("err = %v", err)
	}

	// a branch whose PR is still open is one start never made, or work not landed
	fx = newFinishFixture(t)
	fx.f.Script(testkit.Worktrees, testkit.Porcelain())
	fx.f.Script(testkit.GhView, testkit.PrOpen)

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "has no worktree here") {
		t.Errorf("err = %v", err)
	}
}

// A host that does not answer for a branch with no worktree is reported as
// that, not as a branch start never made: its PR may have merged.
func TestFinishNamesAHostFailureWithoutWorktree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.Worktrees, testkit.Porcelain())
	fx.f.Fail(testkit.GhView, shell.Result{Code: 1, Stderr: "HTTP 502: Bad Gateway"})

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "reading the PR of") || !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v", err)
	}
}

// A finish that stopped after removing the worktree is rerun to the end:
// the PR merged, so the rest is cleaned up and only the branch deleted.
func TestFinishMergedWithoutWorktree(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.Worktrees, testkit.Porcelain())
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.f.Script(testkit.LsRem, "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if !fx.f.Called("git branch -D GH-1") || fx.f.Called("git worktree remove") ||
		fx.f.Called("herdr workspace close") {
		t.Errorf("calls: %v", fx.f.Calls)
	}

	for _, want := range []string{"was already merged", "had no worktree left", "https://gh/pr/9"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

// A rerun with the worktree gone records what a whole finish records: the
// branch size stats reads, the lore line and the riders the merge closed.
func TestFinishMergedWithoutWorktreeKeepsTheRecords(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.Worktrees, testkit.Porcelain())
	fx.f.Script(testkit.GhView, mergedWithBody("Closes #1\nCloses #2"))
	fx.f.Script(testkit.LsRem, "")
	fx.f.Script("git diff --numstat --no-renames origin/main...refs/heads/GH-1", "10\t2\tcli/stats.go")
	fx.in.Task, fx.in.Close, fx.in.HostCloses = testkit.Bundle12, []string{"GH-2"}, true
	fx.in.LoreStore = &lore.Store{Root: filepath.Join(fx.main, ".f10", "lore")}

	var sized []branchSize

	seam.Swap(
		t,
		&recordSize,
		func(_ context.Context, _, _, _ string, s branchSize) { sized = append(sized, s) },
	)

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if len(sized) != 1 || sized[0].Files != 1 {
		t.Errorf("sizes %+v, want the branch's one file", sized)
	}

	if !strings.Contains(out, "lore: no entries for GH-1") {
		t.Errorf("report lacks the lore line:\n%s", out)
	}

	if !regexp.MustCompile(`(?m)^closed:\s+GH-2$`).MatchString(out) {
		t.Errorf("report lacks the closed row:\n%s", out)
	}
}

// A finish marks the task finished in each stopped drive record that lists
// it; a running drive's record and another attempt's row stay as they were.
func TestFinishMarksDriveRecords(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.Root = t.TempDir()

	halted := func() *drivestore.Outcome { return &drivestore.Outcome{Row: "halted at catchup", Code: ui.ExitHalted} }
	for _, tasks := range [][]drivestore.Task{
		{{ID: "GH-1", Outcome: halted()}, {ID: "GH-2", Outcome: halted()}},
		{{ID: "GH-1", Outcome: halted()}},
		{{ID: "GH-1-b", Outcome: halted()}},
	} {
		if err := drivestore.Create(fx.in.Root, &drivestore.Record{Tasks: tasks}); err != nil {
			t.Fatal(err)
		}
	}

	running, err := drivestore.Acquire(fx.in.Root, drivestore.DriveKey(2), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Release()

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		drive int
		id    string
		want  string
	}{
		{1, "GH-1", drivectl.RowFinished},
		{1, "GH-2", "halted at catchup"},
		{2, "GH-1", "halted at catchup"},
		{3, "GH-1-b", "halted at catchup"},
	} {
		rec, err := drivestore.Read(fx.in.Root, c.drive)
		if err != nil {
			t.Fatal(err)
		}

		if o := rec.Task(c.id).Outcome; o == nil || o.Row != c.want {
			t.Errorf("drive %d %s = %+v, want %q", c.drive, c.id, o, c.want)
		}
	}
}

// A finish matches the attempt it acted on: the typed suffix, else the one
// its branch ends in, so a bare finish in GH-1-b's worktree leaves GH-1 alone.
func TestAttemptRows(t *testing.T) {
	rows := []drivestore.Task{{ID: "GH-1"}, {ID: "GH-1-b"}, {ID: "GH-2"}}

	for _, c := range []struct {
		suffix, branch string
		want           []string
	}{
		{"", "GH-1-b", []string{"GH-1-b"}},
		{"", "GH-1", []string{"GH-1"}},
		{"b", "", []string{"GH-1-b"}},
		{"", "feat/GH-1-dark-mode-b", []string{"GH-1-b"}},
	} {
		in := FinishInput{Task: testkit.Gh1, Suffix: c.suffix}
		match := attemptRows(in, c.branch, rows)

		var got []string

		for _, r := range rows {
			if match(r) {
				got = append(got, r.ID)
			}
		}

		if !slices.Equal(got, c.want) {
			t.Errorf("suffix %q branch %q matched %v, want %v", c.suffix, c.branch, got, c.want)
		}
	}
}

// A task with no branch left and its plan archived is already finished.
func TestFinishAlreadyFinished(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.RefsLocal, "")
	fx.in.Storage = storage.Root(t.TempDir())

	if err := os.MkdirAll(filepath.Join(fx.in.Storage.Plans(), "archive"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(fx.in.Storage.Plans(), "archive", "GH-1.1.md"),
		[]byte("# plan"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	fx.in.Root = t.TempDir()

	halted := &drivestore.Outcome{Row: "halted at catchup", Code: ui.ExitHalted}

	rec := &drivestore.Record{Tasks: []drivestore.Task{{ID: "GH-1", Outcome: halted}}}
	if err := drivestore.Create(fx.in.Root, rec); err != nil {
		t.Fatal(err)
	}

	out, err := fx.run(t)
	if err != nil || !strings.Contains(out, "already finished") {
		t.Errorf("err = %v, out:\n%s", err, out)
	}

	rec, err = drivestore.Read(fx.in.Root, 1)
	if err != nil {
		t.Fatal(err)
	}

	if o := rec.Task("GH-1").Outcome; o == nil || o.Row != drivectl.RowFinished {
		t.Errorf("the drive record kept %+v", o)
	}

	// a re-plan archives the old plan too, and keeps the new one live
	if err := os.WriteFile(filepath.Join(fx.in.Storage.Plans(), "GH-1.md"), []byte("# plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "nothing to finish") {
		t.Errorf("a re-planned task with a live plan: err = %v", err)
	}
}

// f10 finish refuses a task a drive holds, naming the drive.
func TestFinishLeaseRefusesADrivenTask(t *testing.T) {
	root := t.TempDir()

	held, err := drivestore.Acquire(root, "GH-1", 3)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Lease(root, "GH-1"); err == nil || !strings.Contains(err.Error(), "owned by drive 3") ||
		!strings.Contains(err.Error(), "f10 drive cancel 3") {
		t.Errorf("err = %v", err)
	}

	held.Release()

	l, err := Lease(root, "GH-1")
	if err != nil {
		t.Fatal(err)
	}

	l.Release()
}

// onMain turns the fixture into a task shipped from the main checkout: the
// branch is checked out in main, and the plan sits in main's .f10/plans.
func (fx *finishFixture) onMain(t *testing.T, plan string) string {
	t.Helper()

	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", fx.main))
	fx.f.Script(testkit.CurBr, "GH-1")
	fx.f.Script("git fetch origin main:main", "")
	fx.f.Script("git switch main", "")

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
// default branch fetched into, then main switched and the branch deleted; no
// worktree removed or workspace closed, even with worktrunk installed.
func TestFinishFromTheMainCheckout(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Has["wt"] = true
	fx.openPR()
	plan := fx.onMain(t, "# plan")
	fx.in.Workspace = "ws:1"

	rec := &recorder{f: fx.f}

	if err := Finish(t.Context(), rec, fx.in); err != nil {
		t.Fatal(err)
	}

	out := rec.buf.String()

	order := []string{
		testkit.GhView,
		testkit.GhMerge,
		testkit.GhView,
		testkit.LsRem,
		testkit.GhDel,
		testkit.DropRemoteRef,
		testkit.CurBr,
		"git fetch origin main:main",
		"git merge-base --is-ancestor m3rge main",
		"git switch main",
		"git branch -D GH-1",
	}

	last := -1

	for _, step := range order {
		idx := testkit.IndexAfter(fx.f.Calls, step, last)
		if idx < 0 {
			t.Fatalf("%q did not run after step %d:\n%s", step, last, strings.Join(fx.f.Calls, "\n"))
		}

		last = idx
	}

	for _, ran := range []string{
		"git pull", "git worktree remove", "wt remove", "herdr workspace close", "herdr workspace focus",
	} {
		if fx.f.Called(ran) {
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

// A branch named by the project's own shape, the id past a prefix with no
// config link, is found by the id it carries.
func TestFinishFindsTheIdPastAPrefix(t *testing.T) {
	const branch = "feat/GH-1-workdays"

	for _, inMain := range []bool{true, false} {
		t.Run(map[bool]string{true: "main checkout", false: "worktree"}[inMain], func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.openPR()
			fx.f.Script(testkit.RefsLocal, "")
			fx.f.Script(testkit.AllHeads, testkit.Heads("main", branch))
			fx.f.Script("git branch -D "+branch, "")

			// the host and origin are asked by the branch's name
			swap := strings.NewReplacer("GH-1 ", branch+" ", "heads/GH-1", "heads/"+branch,
				"origin GH-1", "origin "+branch, "origin/GH-1", "origin/"+branch)
			for _, key := range []string{testkit.GhView, testkit.GhMerge, testkit.GhStack, testkit.GhDel, testkit.LsRem, testkit.DropRemoteRef} {
				fx.f.Answers[swap.Replace(key)] = fx.f.Answers[key]
			}

			plan := fx.plan(t, "# plan")
			if inMain {
				plan = fx.onMain(t, "# plan")
				fx.f.Script(testkit.Worktrees, testkit.Porcelain(branch, fx.main))
				fx.f.Script(testkit.CurBr, branch)
				fx.in.Workspace = "ws:1"
			} else {
				fx.f.Script(testkit.Worktrees, testkit.Porcelain(branch, fx.wt))
			}

			if _, err := fx.run(t); err != nil {
				t.Fatal(err)
			}

			if !fx.f.Called("git branch -D " + branch) {
				t.Errorf("%s was not deleted:\n%s", branch, strings.Join(fx.f.Calls, "\n"))
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

	archived := filepath.Join(fx.main, ".f10", "plans", "archive")
	if err := os.MkdirAll(archived, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"GH-1.md": "first", "GH-1.1.md": "older"} {
		if err := os.WriteFile(filepath.Join(archived, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"GH-1.md": "first", "GH-1.1.md": "older", "GH-1.2.md": "current"} {
		got, err := os.ReadFile(filepath.Join(archived, name))
		if err != nil || string(got) != body {
			t.Errorf("%s = %q, %v; want %q", name, got, err, body)
		}
	}
}

// From the main checkout a fetch that fails is a deferred pull too: main
// still switches to the default branch and the task's branch goes.
func TestFinishFromTheMainCheckoutDefersAFailedFetch(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	plan := fx.onMain(t, "# plan")
	fx.f.Fail("git fetch origin main:main", shell.Result{Code: 1, Stderr: "fatal: refusing to fetch"})

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called("git switch main") || !fx.f.Called("git branch -D GH-1") {
		t.Errorf("calls: %v", fx.f.Calls)
	}

	if !strings.Contains(out, "main not pulled: git fetch: fatal: refusing to fetch") {
		t.Errorf("report:\n%s", out)
	}

	if _, err := os.Stat(plan); err == nil {
		t.Error("the plan was not archived")
	}
}

// A switch or a delete git refuses at the end names the merge that landed
// and what is left.
func TestFinishFromTheMainCheckoutTailFails(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.onMain(t, "# plan")
	fx.f.Fail("git switch main", shell.Result{Code: 1, Stderr: "error: cannot switch"})

	want := "https://gh/pr/9 merged, but switching the main checkout to main failed"
	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}

	if fx.f.Called("git branch -D") {
		t.Error("the branch was deleted although the switch failed")
	}

	fx = newFinishFixture(t)
	fx.openPR()
	fx.onMain(t, "# plan")
	fx.f.Fail("git branch -D GH-1", shell.Result{Code: 1, Stderr: "error: ref locked"})

	want = "https://gh/pr/9 merged, but main is on main, and deleting GH-1 failed (git branch -D GH-1)"
	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}
}

// A dirty main checkout is refused before the host is reached.
func TestFinishFromTheMainCheckoutRefusesDirty(t *testing.T) {
	fx := newFinishFixture(t)
	fx.onMain(t, "# plan")
	fx.f.Script("git status --porcelain", " M finish.go")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("err = %v", err)
	}

	if fx.f.Called("gh ") || fx.f.Called("git switch") {
		t.Errorf("the host or a switch was reached before the dirty check: %v", fx.f.Calls)
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
	fx.f.Script(testkit.GhView, prOpenBy("alice"))
	fx.f.Script(ghUser, `{"login":"me"}`)

	_, err := fx.run(t)

	fp := ForeignPullError{}
	if !errors.As(err, &fp) || fp.Author != "alice" || !strings.Contains(err.Error(), "--merge") ||
		!strings.Contains(err.Error(), "--local") {
		t.Fatalf("err = %v, want the foreign PR refusal naming both flags", err)
	}

	if fx.f.Called("gh pr merge") || fx.f.Called("git worktree remove") || fx.f.Called("herdr workspace close") {
		t.Errorf("finish changed something on a refusal: %v", fx.f.Calls)
	}
}

// One's own PR merges, whatever case the host spells the login in.
func TestFinishMergesOwnPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Script(testkit.GhView, prOpenBy("Me"), testkit.PrMerged)
	fx.f.Script(ghUser, `{"login":"me"}`)

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called(testkit.GhMerge) {
		t.Error("the user's own PR was not merged")
	}
}

// --merge merges someone else's PR without asking who the user is.
func TestFinishMergeFlagMergesSomeoneElsesPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Script(testkit.GhView, prOpenBy("alice"), testkit.PrMerged)
	fx.in.Flags.Merge = true

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called(testkit.GhMerge) || fx.f.Called(ghUser) {
		t.Errorf("--merge = %v, want a merge and no login read", fx.f.Calls)
	}
}

// --local leaves the PR open: no merge, no remote delete, no pull, no
// dependents released, and the plan, worktree, branch and workspace go.
func TestFinishLocalLeavesThePROpen(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, prOpenBy("alice"))
	fx.f.Script(unpushed, "")
	fx.plan(t, "# plan")
	fx.in.Flags.LeaveOpen = true
	released := fx.dependent()

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	for _, banned := range []string{"gh pr merge", testkit.GhDel, testkit.LsRem, "git pull", ghUser} {
		if fx.f.Called(banned) {
			t.Errorf("--local ran %q: %v", banned, fx.f.Calls)
		}
	}

	if released() {
		t.Error("--local released a dependent")
	}

	for _, want := range []string{"git worktree remove " + fx.wt, "git branch -D GH-1", "herdr workspace close ws:9"} {
		if !fx.f.Called(want) {
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
	fx.f.Script(testkit.GhView, prOpenBy("alice"))
	fx.f.Fail(unpushed, testkit.Missing)
	fx.in.Flags.LeaveOpen = true

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "push or drop them") {
		t.Fatalf("err = %v, want the unpushed commits refused", err)
	}

	if fx.f.Called("git worktree remove") || fx.f.Called("git branch -D") {
		t.Errorf("the branch went although it had unpushed commits: %v", fx.f.Calls)
	}
}

// A closed PR is refused without --local, and the refusal names it.
func TestFinishClosedPRNamesLocal(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, `{"state":"CLOSED","url":"https://gh/pr/9","number":9,"baseRefName":"main"}`)

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "--local") {
		t.Fatalf("err = %v, want the closed PR refused, naming --local", err)
	}
}

// noPull scripts a branch never pushed: no request, no copy on origin, and
// the merge built in memory on main's head.
func (fx *finishFixture) noPull() {
	fx.f.Fail(testkit.GhView, shell.Result{Code: 1, Stderr: `no pull requests found for branch "GH-1"`})
	fx.f.Script(testkit.LsRem, "")
	fx.f.Script("git config --get branch.GH-1.merge", "")
	fx.f.Script("git rev-parse -q --verify refs/heads/main", "m0")
	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", "m0")
	fx.f.Script("git rev-parse -q --verify refs/heads/GH-1", "t1")
	fx.f.Script("git merge-tree --write-tree m0 refs/heads/GH-1", "tr33\n")
	fx.f.Script("git rev-parse m0^{tree}", "old7ree")
	fx.f.Script("git log --reverse --format=%s m0..refs/heads/GH-1", "first\nsecond")
}

// A branch never pushed is squashed into the local default branch here:
// built on main's head, main fast-forwarded, nothing pushed, pulled or
// deleted on origin.
func TestFinishMergesABranchNeverPushedHere(t *testing.T) {
	for _, onDefault := range []bool{true, false} {
		t.Run(fmt.Sprintf("onDefault=%v", onDefault), func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.noPull()
			fx.in.Method = "squash"
			fx.in.Title = func(context.Context) (string, error) { return "Add the thing", nil }
			fx.f.Script("git commit-tree tr33 -p m0 -m GH-1: Add the thing -m - first\n- second", "s9")
			fx.f.Script("git merge --ff-only s9", "")
			fx.f.Script("git update-ref refs/heads/main s9 m0", "")

			advance := "git merge --ff-only s9"
			if !onDefault {
				fx.f.Script(testkit.CurBr, "elsewhere")
				advance = "git update-ref refs/heads/main s9 m0"
			}

			released := fx.dependent()

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

			if !fx.f.Called(advance) {
				t.Errorf("%q did not run: %q", advance, fx.f.Calls)
			}

			for _, never := range []string{"git push", "git pull", "gh pr merge", testkit.GhDel, testkit.DropRemoteRef, "git fetch"} {
				if fx.f.Called(never) {
					t.Errorf("%q ran on a local finish", never)
				}
			}

			if !released() {
				t.Error("the dependent was not released")
			}

			for _, step := range []string{"git worktree remove " + fx.wt, "git branch -D GH-1"} {
				if !fx.f.Called(step) {
					t.Errorf("%q did not run", step)
				}
			}
		})
	}
}

// On a host-issues tracker the local commit closes the task once it reaches
// origin's default branch, above the trailers with traces on, with or without
// them; the rebase method writes no commit, so the task stays for the tracker.
func TestFinishLocalCommitClosesTheTask(t *testing.T) {
	cases := []struct {
		name, method string
		traces       bool
		commit, sha  string
	}{
		{
			name:   "squash",
			method: "squash",
			commit: "git commit-tree tr33 -p m0 -m GH-1: Add the thing -m - first\n- second\n\nCloses #1",
			sha:    "s9",
		},
		{
			name:   "squash with traces",
			method: "squash",
			traces: true,
			commit: "git commit-tree tr33 -p m0 -m GH-1: Add the thing -m - first\n- second\n\nCloses #1\n\nTask: GH-1\n",
			sha:    "s9",
		},
		{
			name:   "merge",
			method: "merge",
			commit: "git commit-tree tr33 -p m0 -p t1 -m Merge branch 'GH-1' -m Closes #1",
			sha:    "mm",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.noPull()
			fx.in.Method, fx.in.Traces, fx.in.HostCloses = c.method, c.traces, true
			fx.in.Title = func(context.Context) (string, error) { return "Add the thing", nil }
			fx.f.Script(c.commit, c.sha)
			fx.f.Script("git merge --ff-only "+c.sha, "")

			out, err := fx.run(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			if !fx.f.Called(c.commit) {
				t.Errorf("%q did not run: %q", c.commit, fx.f.Calls)
			}

			if !strings.Contains(out, "GH-1 closes when main reaches origin") || strings.Contains(out, "no PR closes") {
				t.Errorf("the note does not say the push closes the task:\n%s", out)
			}
		})
	}

	fx := newFinishFixture(t)
	fx.noPull()
	fx.in.Method, fx.in.HostCloses = "rebase", true
	fx.f.Script("git merge-base --is-ancestor m0 refs/heads/GH-1", "")
	fx.f.Script("git merge --ff-only t1", "")

	if out, err := fx.run(t); err != nil || !strings.Contains(out, "no PR closes GH-1") {
		t.Fatalf("rebase: %v\n%s", err, out)
	}
}

// Each member the merge closes gets a line: the lead, and the riders --close
// names; a rider kept open, or a tracker that is not the host's, gets none.
func TestClosingLines(t *testing.T) {
	in := FinishInput{Task: testkit.Bundle12, HostCloses: true}
	if got := closingLines(in); got != "Closes #1" {
		t.Errorf("rider kept open: %q", got)
	}

	in.Close = []string{"GH-2"}
	if got := closingLines(in); got != "Closes #1\nCloses #2" {
		t.Errorf("rider closed: %q", got)
	}

	in.HostCloses = false
	if got := closingLines(in); got != "" {
		t.Errorf("not the host's issues, or a driver's tracker: %q", got)
	}
}

// A rerun after a landing finds the branch already in the default branch:
// the unpushed commit that closes the task makes the note say so.
func TestFinishLocalRerunKeepsTheClosingNote(t *testing.T) {
	for _, landed := range []string{"GH-1: Add the thing\n\nCloses #1\n", "GH-1: Add the thing\n"} {
		fx := newFinishFixture(t)
		fx.noPull()
		fx.in.HostCloses = true
		fx.f.Script("git rev-parse m0^{tree}", "tr33")
		fx.f.Script("git log --format=%B refs/remotes/origin/main..refs/heads/main", landed)

		out, err := fx.run(t)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		want := "GH-1 closes when main reaches origin"
		if !strings.Contains(landed, "Closes") {
			want = "no PR closes GH-1"
		}

		if !strings.Contains(out, want) {
			t.Errorf("landed %q: report lacks %q:\n%s", landed, want, out)
		}
	}
}

// The merge and rebase methods land a branch never pushed too: a merge
// commit with both parents, a fast-forward to the branch's own head.
func TestFinishLandsANeverPushedBranchByMethod(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.in.Method = "merge"
	fx.f.Script("git commit-tree tr33 -p m0 -p t1 -m Merge branch 'GH-1'", "mm")
	fx.f.Script("git merge --ff-only mm", "")

	if out, err := fx.run(t); err != nil || !strings.Contains(out, "mm") {
		t.Fatalf("merge: %v\n%s", err, out)
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.in.Method = "rebase"
	fx.f.Script("git merge-base --is-ancestor m0 refs/heads/GH-1", "")
	fx.f.Script("git merge --ff-only t1", "")

	if out, err := fx.run(t); err != nil || !strings.Contains(out, "t1") {
		t.Fatalf("rebase: %v\n%s", err, out)
	}

	if fx.f.Called("git commit-tree") {
		t.Error("a fast-forward wrote a commit")
	}
}

// A branch never pushed that conflicts with the default branch, or is
// behind it under the rebase method, is refused with nothing changed and
// the catchup named; one already in the default branch lands nothing.
func TestFinishRefusesANeverPushedBranchItCannotLand(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.f.Fail("git merge-tree --write-tree m0 refs/heads/GH-1", shell.Result{Code: 1})

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "conflicts with main: run /f10:catchup") {
		t.Fatalf("conflict: err = %v", err)
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.in.Method = "rebase"
	fx.f.Fail("git merge-base --is-ancestor m0 refs/heads/GH-1", shell.Result{Code: 1})

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "lands only a fast-forward") {
		t.Fatalf("behind: err = %v", err)
	}

	for _, never := range []string{"git commit-tree", "git update-ref refs/heads/main", "git merge --ff-only", "git worktree remove"} {
		if fx.f.Called(never) {
			t.Errorf("%q ran on a refusal", never)
		}
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.f.Script("git rev-parse m0^{tree}", "tr33")

	out, err := fx.run(t)
	if err != nil || !strings.Contains(out, "GH-1 was already in main") || fx.f.Called("git commit-tree") {
		t.Fatalf("rerun: %v\n%s", err, out)
	}
}

// A pushed branch with no PR stays a refusal, naming both ways out.
func TestFinishRefusesAPushedBranchWithoutAPR(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Fail(testkit.GhView, shell.Result{Code: 1, Stderr: `no pull requests found for branch "GH-1"`})

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
	fx.in.Method = "squash"
	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", "o1")
	fx.f.Fail("git merge-base --is-ancestor o1 m0", shell.Result{Code: 1})
	fx.f.Script("git merge-base --is-ancestor m0 o1", "")
	fx.f.Script("git merge-tree --write-tree o1 refs/heads/GH-1", "tr33")
	fx.f.Script("git rev-parse o1^{tree}", "o7ree")
	fx.f.Script("git log --reverse --format=%s o1..refs/heads/GH-1", "only")
	fx.f.Script("git commit-tree tr33 -p o1 -m only", "s9")
	fx.f.Script("git merge --ff-only s9", "")

	if out, err := fx.run(t); err != nil || !strings.Contains(out, "s9") {
		t.Fatalf("behind: %v\n%s", err, out)
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", "o1")
	fx.f.Fail("git merge-base --is-ancestor o1 m0", shell.Result{Code: 1})
	fx.f.Fail("git merge-base --is-ancestor m0 o1", shell.Result{Code: 1})

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "main and origin/main have diverged") {
		t.Fatalf("diverged: err = %v", err)
	}

	if fx.f.Called("git commit-tree") || fx.f.Called("git worktree remove") {
		t.Error("a diverged main was merged into")
	}
}

// --local merges nothing, so a branch with no PR is still refused, and so
// is one tracking an upstream origin lacks, which may be a request the
// host merged and no longer lists by branch.
func TestFinishKeepsTheRefusalWhenNeverPushedIsNotProven(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.in.Flags.LeaveOpen = true

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "no pull requests found") {
		t.Fatalf("--local: err = %v", err)
	}

	fx = newFinishFixture(t)
	fx.noPull()
	fx.f.Script("git config --get branch.GH-1.merge", "refs/heads/GH-1")

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "tracks a branch origin no longer has") {
		t.Fatalf("upstream: err = %v", err)
	}

	if fx.f.Called("git commit-tree") || fx.f.Called("git merge-tree") {
		t.Error("a branch not proven unpushed was merged")
	}
}

// In-repo storage every checkout shares: the plan in main's plans dir is
// archived beside it, and the worktree holds nothing to move.
func TestFinishArchivesMainsSharedPlan(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()

	shared := filepath.Join(fx.main, ".f10", "plans")
	fx.in.Storage = storage.InRepo(fx.main)

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

// Finish records the branch's size against the base it forked from,
// lockfiles left out and the markdown share kept; nothing to measure records
// nothing.
func TestFinishRecordsTheBranchSize(t *testing.T) {
	const (
		numstat = "10\t2\tcli/stats.go\n5\t1\tdocs/cli.md\n40\t0\tgo.sum\n-\t-\tlogo.png"
		diff    = "git diff --numstat --no-renames "
	)

	sized := func(base string) *branchSize {
		return &branchSize{Lines: 18, Md: 6, Files: 3, Base: base}
	}

	for _, tc := range []struct {
		name  string
		setup func(fx *finishFixture)
		want  *branchSize
	}{
		{
			name:  "against the default branch",
			setup: func(fx *finishFixture) { fx.f.Script(diff+"origin/main...refs/heads/GH-1", numstat) },
			want:  sized("origin/main"),
		},
		{
			name: "against its base branch",
			setup: func(fx *finishFixture) {
				fx.f.Meta.Seed("GH-1", branchmeta.AfterBranch, "GH-0")
				fx.f.Script(diff+"GH-0...refs/heads/GH-1", numstat)
			},
			want: sized("GH-0"),
		},
		{
			name: "a base branch already deleted falls back to the default branch",
			setup: func(fx *finishFixture) {
				fx.f.Meta.Seed("GH-1", branchmeta.AfterBranch, "GH-0")
				fx.f.Fail(diff+"GH-0...refs/heads/GH-1", shell.Result{Code: 128})
				fx.f.Script(diff+"origin/main...refs/heads/GH-1", numstat)
			},
			want: sized("origin/main"),
		},
		{
			name: "a PR merged with a merge commit",
			setup: func(fx *finishFixture) {
				fx.f.Script(diff+"origin/main...refs/heads/GH-1", "")
				fx.f.Script("git rev-list --ancestry-path --merges --reverse refs/heads/GH-1..origin/main", "mc1\nmc2")
				fx.f.Script(diff+"mc1^1...refs/heads/GH-1", numstat)
			},
			want: sized("mc1^1"),
		},
		{
			name: "a branch never pushed",
			setup: func(fx *finishFixture) {
				fx.noPull()
				fx.in.Method = "merge"
				fx.f.Script("git commit-tree tr33 -p m0 -p t1 -m Merge branch 'GH-1'", "mm")
				fx.f.Script("git merge --ff-only mm", "")
				fx.f.Script(diff+"main...refs/heads/GH-1", numstat)
			},
			want: sized("main"),
		},
		{
			name: "a branch never pushed, already landed",
			setup: func(fx *finishFixture) {
				fx.noPull()
				fx.f.Script("git rev-parse m0^{tree}", "tr33")
			},
		},
		{
			name:  "no base resolves",
			setup: func(fx *finishFixture) { fx.f.Fail(diff+"origin/main...refs/heads/GH-1", shell.Result{Code: 128}) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.openPR()
			tc.setup(fx)

			var got []branchSize

			seam.Swap(t, &recordSize, func(_ context.Context, _, branch, task string, s branchSize) {
				if branch != "GH-1" || task != "GH-1" {
					t.Errorf("recorded for %s on %s", task, branch)
				}

				got = append(got, s)
			})

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
	fx.f.Script("sweep", "")
	fx.in.Sweep = func(context.Context) { fx.f.Answer("sweep") }

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	removed := slices.Index(fx.f.Calls, "git branch -D GH-1")
	swept := slices.Index(fx.f.Calls, "sweep")
	closed := slices.Index(fx.f.Calls, "herdr workspace close ws:9")

	if removed < 0 || swept < removed || closed < swept {
		t.Errorf("branch removed at %d, swept at %d, workspace closed at %d", removed, swept, closed)
	}
}

// A bare finish is the branch's task, over the session's; a typed id still
// splits its suffix, and on the default branch, or under -C, nothing
// finishes unasked, the session's task included.
func TestFinishRef(t *testing.T) {
	repo := testkit.TracedRepo(t, "DM-5/fix", "")
	testkit.SessionTask(t, "DM-9")

	tg, err := target.At(t.Context(), repo, false, "finish")
	if err != nil {
		t.Fatal(err)
	}

	if r, suffix, _, err := Ref(t.Context(), tg, ""); err != nil || r.ID != "DM-5" || suffix != "" {
		t.Errorf("bare = %q %q %v, want DM-5 and no suffix", r.ID, suffix, err)
	}

	if _, _, branch, _ := Ref(t.Context(), tg, ""); branch != "DM-5/fix" {
		t.Errorf("bare branch = %q, want the one checked out here", branch)
	}

	if r, suffix, _, err := Ref(t.Context(), tg, "DM-7-b"); err != nil || r.ID != "DM-7" || suffix != "b" {
		t.Errorf("DM-7-b = %q %q %v, want DM-7 and suffix b", r.ID, suffix, err)
	}

	scoped, err := target.At(t.Context(), repo, true, "finish")
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = Ref(t.Context(), scoped, "")
	if err == nil || !strings.Contains(err.Error(), "explicit task id") {
		t.Errorf("bare under -C: err = %v, want a refusal", err)
	}

	onMain := testkit.TracedRepo(t, "main", "")
	testkit.SessionTask(t, "DM-9")

	tg, err = target.At(t.Context(), onMain, false, "finish")
	if err != nil {
		t.Fatal(err)
	}

	r, _, _, err := Ref(t.Context(), tg, "")
	if err == nil || !strings.Contains(err.Error(), "branch names no task") {
		t.Errorf("bare on main with a session task = %q, %v; want a refusal", r.ID, err)
	}
}

// A bare call in a suffixed task's worktree finishes that branch, not the
// unsuffixed sibling the id alone would find.
func TestFinishTargetTakesTheBareCallsBranch(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.RefsLocal, testkit.Heads("GH-1", "GH-1-b"))
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", fx.main+".other", "GH-1-b", fx.wt))
	fx.in.Branch = "GH-1-b"

	name, path, _, err := finishTarget(t.Context(), fx.in)
	if err != nil || name != "GH-1-b" || path != fx.wt {
		t.Errorf("finishTarget = %q %q %v, want GH-1-b at %s", name, path, err, fx.wt)
	}
}

// finish names each action as it starts, for drive's record: the merge by
// its PR, or the branch it lands here, then the local cleanup.
func TestFinishReportsProgress(t *testing.T) {
	t.Run("pr", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()

		var got []string

		fx.in.Progress = func(a string) { got = append(got, a) }

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
		fx.in.Method = "merge"
		fx.f.Script("git commit-tree tr33 -p m0 -p t1 -m Merge branch 'GH-1'", "mm")
		fx.f.Script("git merge --ff-only mm", "")

		var got []string

		fx.in.Progress = func(a string) { got = append(got, a) }

		if out, err := fx.run(t); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		if len(got) == 0 || got[0] != "merging GH-1 into main" || slices.Contains(got, "pulling main") {
			t.Errorf("progress = %q, want a local merge first and no pull", got)
		}
	})
}

// The watcher's finish never merges: an open PR is refused before anything
// moves, and a merged one finishes as any finish would.
func TestFinishMergedOnly(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Flags.MergedOnly = true
	fx.openPR()

	if _, err := fx.run(t); !errors.Is(err, errNotMerged) {
		t.Fatalf("err = %v, want errNotMerged", err)
	}

	for _, p := range []string{"gh pr merge", "git worktree remove", "herdr workspace close", "git branch -D"} {
		if fx.f.Called(p) {
			t.Errorf("%q ran for an open PR", p)
		}
	}

	fx = newFinishFixture(t)
	fx.in.Flags.MergedOnly = true
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.f.Script(testkit.LsRem, "")

	if _, err := fx.run(t); err != nil {
		t.Fatal(err)
	}

	if !fx.f.Called("git worktree remove") {
		t.Errorf("a merged PR was not finished: %v", fx.f.Calls)
	}
}

// The lore check runs right before the merge lands: warn notes and merges,
// block errors with nothing changed; nothing about to merge runs no check.
func TestFinishLoreGate(t *testing.T) {
	storeIn := func(fx *finishFixture) *lore.Store {
		return &lore.Store{Root: filepath.Join(fx.main, ".f10", "lore")}
	}

	t.Run("warn", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()
		fx.in.LoreStore, fx.in.Gate = storeIn(fx), "warn"

		out, err := fx.run(t)
		if err != nil {
			t.Fatal(err)
		}

		if !fx.f.Called("gh pr merge") ||
			!strings.Contains(out, "lore check GH-1: no entries for GH-1 (lore_gate: warn): merged anyway") {
			t.Errorf("warn did not merge with a note:\n%s", out)
		}
	})

	t.Run("block", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()
		plan := fx.plan(t, "# plan")
		fx.in.LoreStore, fx.in.Gate = storeIn(fx), "block"

		_, err := fx.run(t)
		if err == nil || !strings.Contains(err.Error(), "lore check GH-1: no entries for GH-1 (lore_gate: block)") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.Called("gh pr merge") || fx.f.Called("git worktree remove") {
			t.Errorf("block changed something: %v", fx.f.Calls)
		}

		if _, err := os.Stat(plan); err != nil {
			t.Error("the plan moved")
		}
	})

	t.Run("already merged", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.f.Script(testkit.GhView, testkit.PrMerged)
		fx.f.Script(testkit.LsRem, "")
		fx.in.LoreStore, fx.in.Gate = storeIn(fx), "block"

		if out, err := fx.run(t); err != nil || strings.Contains(out, "lore check") {
			t.Errorf("a merged PR was checked: %v\n%s", err, out)
		}
	})

	t.Run("local", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.f.Script(testkit.GhView, prOpenBy("alice"))
		fx.f.Script(unpushed, "")
		fx.in.Flags.LeaveOpen = true
		fx.in.LoreStore, fx.in.Gate = storeIn(fx), "block"

		if out, err := fx.run(t); err != nil || strings.Contains(out, "lore check") {
			t.Errorf("--local was checked: %v\n%s", err, out)
		}
	})

	// a branch never pushed lands here, and is gated the same way
	neverPushed := func(t *testing.T, gate string) (*finishFixture, string, error) {
		t.Helper()

		fx := newFinishFixture(t)
		fx.noPull()
		fx.in.Method = "squash"
		fx.in.Title = func(context.Context) (string, error) { return "Add the thing", nil }
		fx.f.Script("git commit-tree tr33 -p m0 -m GH-1: Add the thing -m - first\n- second", "s9")
		fx.f.Script("git merge --ff-only s9", "")
		fx.in.LoreStore, fx.in.Gate = storeIn(fx), gate

		out, err := fx.run(t)

		return fx, out, err
	}

	t.Run("never pushed, warn", func(t *testing.T) {
		fx, out, err := neverPushed(t, "warn")
		if err != nil {
			t.Fatal(err)
		}

		if !fx.f.Called("git merge --ff-only s9") ||
			!strings.Contains(out, "lore check GH-1: no entries for GH-1 (lore_gate: warn): merged anyway") {
			t.Errorf("warn did not land the branch with a note:\n%s", out)
		}
	})

	t.Run("never pushed, block", func(t *testing.T) {
		fx, _, err := neverPushed(t, "block")
		if err == nil || !strings.Contains(err.Error(), "(lore_gate: block)") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.Called("git commit-tree") || fx.f.Called("git merge --ff-only") || fx.f.Called("git worktree remove") {
			t.Errorf("block landed or removed something: %v", fx.f.Calls)
		}
	})

	// the first PR's open major finding belongs to its own series
	t.Run("a follow-up checks its own series", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()
		store := storeIn(fx)
		fx.in.LoreStore, fx.in.Gate = store, "warn"

		if err := os.MkdirAll(filepath.Join(fx.main, ".f10", "reviews", "archive", "GH-1.1"), 0o750); err != nil {
			t.Fatal(err)
		}

		finding := lore.Entry{
			Kind: lore.KindFinding, Change: "GH-1", Human: "dev@example.com",
			Severity: "major", Status: "open", Source: "local", Body: "Breaks the thing",
		}
		if _, err := store.Add(t.Context(), finding); err != nil {
			t.Fatal(err)
		}

		out, err := fx.run(t)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(out, "lore check GH-1.2: no entries for GH-1.2 (lore_gate: warn)") ||
			strings.Contains(out, "Breaks the thing") {
			t.Errorf("the follow-up was not checked on its own series:\n%s", out)
		}
	})

	t.Run("passes", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()
		store := storeIn(fx)
		fx.in.LoreStore, fx.in.Gate = store, "block"

		for _, e := range []lore.Entry{
			{Kind: lore.KindIntent, Change: "GH-1", Human: "dev@example.com", Body: "Why"},
			{
				Kind: lore.KindVerdict, Change: "GH-1", Human: "dev@example.com",
				Outcome: "proceed", Gate: "judge", Commit: "abcdef1", Body: "Fine",
			},
		} {
			if _, err := store.Add(t.Context(), e); err != nil {
				t.Fatal(err)
			}
		}

		out, err := fx.run(t)
		if err != nil || !fx.f.Called("gh pr merge") || strings.Contains(out, "lore check") {
			t.Errorf("a passing check left a trace: %v\n%s", err, out)
		}
	})
}

// A parent delivered through its subtasks has no branch and no plan of its
// own: finish closes it once every subtask is closed and branchless.
func TestFinishParentClosesWhenSubtasksAreDone(t *testing.T) {
	fx := newParentFixture(t, stack.ChildRow{ID: "GH-2", State: "closed"}, stack.ChildRow{ID: "GH-3", State: "CLOSED"})
	fx.f.Script("gh issue close 1", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("err = %v, out:\n%s", err, out)
	}

	if !fx.f.Called("gh issue close 1") {
		t.Error("the parent was not closed")
	}

	if !strings.Contains(out, "GH-2, GH-3") {
		t.Errorf("the report does not name the subtasks:\n%s", out)
	}
}

// A subtask open, or closed with a branch left, keeps the parent open, and
// the refusal names it; a stale tracking ref is named so it can be pruned.
func TestFinishParentRefusesUnfinishedSubtasks(t *testing.T) {
	fx := newParentFixture(t, stack.ChildRow{ID: "GH-2", State: "open"}, stack.ChildRow{ID: "GH-3", State: "closed"})
	fx.f.Script("git for-each-ref --format=%(refname) refs/remotes/origin/GH-3 refs/remotes/origin/GH-3-* "+
		"refs/remotes/origin/GH-3+* refs/remotes/origin/GH-3+*/*", "refs/remotes/origin/GH-3")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "GH-2 is open") ||
		!strings.Contains(err.Error(), "GH-3 still has branch origin/GH-3") {
		t.Errorf("err = %v", err)
	}

	if fx.f.Called("gh issue close") {
		t.Error("the parent was closed")
	}
}

// No subtasks, or no parent link, keeps today's refusal.
func TestFinishNoBranchNoSubtasksRefuses(t *testing.T) {
	for name, kids := range map[string]func(context.Context, ref.Ref) ([]stack.ChildRow, error){
		"none":           func(context.Context, ref.Ref) ([]stack.ChildRow, error) { return nil, nil },
		"no parent link": func(context.Context, ref.Ref) ([]stack.ChildRow, error) { return nil, tasksource.ErrNoParentLink },
	} {
		fx := newParentFixture(t)
		fx.in.Subtasks = kids

		if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "nothing to finish") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// A tracker that is not the host closes nothing: the note says where to.
func TestFinishParentOnAnotherTracker(t *testing.T) {
	fx := newParentFixture(t, stack.ChildRow{ID: "GH-2", State: "closed"})
	fx.in.CloseTask = nil

	out, err := fx.run(t)
	if err != nil || !strings.Contains(out, "close GH-1 there") {
		t.Errorf("err = %v, out:\n%s", err, out)
	}

	if fx.f.Called("gh issue close") {
		t.Error("an issue was closed")
	}
}

// An open subtask drive already counts as finished is still refused, and
// a close the host rejects fails the finish.
func TestFinishParentArchivedButOpenAndFailedClose(t *testing.T) {
	fx := newParentFixture(t, stack.ChildRow{ID: "GH-2", State: "open", Finished: true})
	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "open in the tracker though its plan is archived") {
		t.Errorf("err = %v", err)
	}

	fx = newParentFixture(t, stack.ChildRow{ID: "GH-2", State: "closed"})
	fx.f.Answers["gh issue close 1"] = []shell.Result{{Code: 1, Stderr: "boom"}}

	if _, err := fx.run(t); err == nil || !strings.Contains(err.Error(), "closing GH-1") {
		t.Errorf("a failed close: err = %v", err)
	}
}

// Only the host's issues get a close; a declared tracker of another kind gets nil.
func TestCloseTaskFor(t *testing.T) {
	tg := testkit.ChildTarget(t, "gh")
	if closeTaskFor(tg, hostops.NewHost("gh", tg.Dir)) == nil {
		t.Error("host issues: no close")
	}

	tg.Effective.TrackerKind = facts.TrackerJira
	tg.Effective.Facts = append(tg.Effective.Facts, facts.Fact{Key: "tracker.kind", Origin: facts.DeclaredIn("main")})

	if closeTaskFor(tg, hostops.NewHost("gh", tg.Dir)) != nil {
		t.Error("a Jira tracker: a close was bound")
	}
}

func newParentFixture(t *testing.T, kids ...stack.ChildRow) *finishFixture {
	t.Helper()

	fx := newFinishFixture(t)
	fx.f.Script(testkit.RefsLocal, "")
	fx.in.Storage = storage.Root(t.TempDir())
	fx.in.Subtasks = func(context.Context, ref.Ref) ([]stack.ChildRow, error) { return kids, nil }
	fx.in.CloseTask = func(ctx context.Context, r ref.Ref) error { return fx.in.Host.SetIssueOpen(ctx, r.Number, false) }

	for _, k := range kids {
		fx.f.Script(strings.ReplaceAll(testkit.RefsLocal, "GH-1", k.ID), "")
	}

	return fx
}

// A merge prints a current handoff under its report, hands it to the caller
// and archives it beside the plan.
func TestFinishPrintsAndArchivesTheHandoff(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Script("git rev-parse --verify -q refs/heads/GH-1", "abc1234567")

	src := archive.TaskHandoff(fx.in.Storage, "GH-1")
	testkit.WriteHandoffFile(t, src, "abc1234567")

	var handed []string

	fx.in.Handoff = func(lines []string) { handed = lines }

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	archived := filepath.Join(filepath.Dir(src), "archive", "GH-1.md")
	if fsx.Exists(src) || !fsx.Exists(archived) {
		t.Errorf("handoff not archived to %s:\n%s", archived, out)
	}

	if !strings.Contains(out, "handoff:") || !strings.Contains(out, archived) ||
		!strings.Contains(out, "\n\n"+testkit.HandoffBody+"\n") || strings.Contains(out, "written at") {
		t.Errorf("report:\n%s", out)
	}

	if strings.Join(handed, "\n") != testkit.HandoffBody {
		t.Errorf("handed = %q", handed)
	}
}

// A stale handoff says so, and a missing one names what to check; neither
// stops the finish.
func TestFinishCompletesOnAStaleOrMissingHandoff(t *testing.T) {
	for name, want := range map[string]string{
		"stale":   "written at abc1234, the branch merged at def7654",
		"missing": "no handoff for GH-1:",
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.openPR()

			if name == "stale" {
				fx.f.Script("git rev-parse --verify -q refs/heads/GH-1", "def7654321")
				testkit.WriteHandoffFile(t, archive.TaskHandoff(fx.in.Storage, "GH-1"), "abc1234567")
			}

			out, err := fx.run(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			if !strings.Contains(out, want) || !fx.f.Called("git branch -D GH-1") {
				t.Errorf("want %q and a finished task:\n%s", want, out)
			}
		})
	}
}

// A PR left open ships nothing: its handoff is archived and not printed.
func TestFinishLocalArchivesTheHandoffSilently(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, prOpenBy("alice"))
	fx.f.Script(unpushed, "")
	fx.in.Flags.LeaveOpen = true

	src := archive.TaskHandoff(fx.in.Storage, "GH-1")
	testkit.WriteHandoffFile(t, src, "abc")

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if fsx.Exists(src) || strings.Contains(out, testkit.HandoffBody) || strings.Contains(out, "handoff") {
		t.Errorf("--local printed or kept the handoff:\n%s", out)
	}
}

// A rerun after the worktree went prints and archives the handoff as a
// full finish does, comparing it with the branch still held here.
func TestFinishMergedWithoutWorktreePrintsTheHandoff(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.Worktrees, testkit.Porcelain())
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.f.Script(testkit.LsRem, "")
	fx.f.Script("git rev-parse --verify -q refs/heads/GH-1", "abc1234567")

	src := archive.TaskHandoff(fx.in.Storage, "GH-1")
	testkit.WriteHandoffFile(t, src, "abc1234567")

	var handed []string

	fx.in.Handoff = func(lines []string) { handed = lines }

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	archived := filepath.Join(filepath.Dir(src), "archive", "GH-1.md")
	if fsx.Exists(src) || !fsx.Exists(archived) || !strings.Contains(out, "handoff:") ||
		!strings.Contains(out, archived) ||
		!strings.Contains(out, "\n\n"+testkit.HandoffBody+"\n") ||
		strings.Join(handed, "\n") != testkit.HandoffBody {
		t.Errorf("handed = %q, report:\n%s", handed, out)
	}
}

// With no worktree left, a deferred pull and a plan that cannot be archived
// both run on: the branch stays for the rerun, which completes.
func TestFinishMergedWithoutWorktreeRunsOnAndKeepsTheBranch(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.Worktrees, testkit.Porcelain())
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.f.Script(testkit.LsRem, "")
	fx.f.Fail("git pull --ff-only", shell.Result{Code: 1, Stderr: "fatal: Not possible to fast-forward, aborting."})

	plans := filepath.Join(fx.main, ".f10", "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(plans, "GH-1.md"), []byte("# plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	blocker := filepath.Join(plans, "archive")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "archiving the plan failed") ||
		!strings.Contains(err.Error(), "the branch stays") || !strings.Contains(err.Error(), "rerun f10 finish GH-1") {
		t.Fatalf("err = %v, want the archive named, the branch kept and a rerun", err)
	}

	if fx.f.Called("git branch -D") || !strings.Contains(out, "main not pulled") {
		t.Errorf("calls: %v\nreport:\n%s", fx.f.Calls, out)
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}

	fx.f.Calls = nil
	fx.f.Script("git pull --ff-only", "")

	if out, err := fx.run(t); err != nil {
		t.Fatalf("rerun: %v\n%s", err, out)
	}

	if !fx.f.Called("git branch -D GH-1") || !storage.Root(filepath.Dir(plans)).PlanArchived("GH-1") {
		t.Errorf("rerun calls: %v", fx.f.Calls)
	}
}

// A worktree git will not remove is named, the workspace still closes, and
// the rerun on the merged PR removes it.
func TestFinishRerunsAfterAFailedRemoval(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Fail("git worktree remove "+fx.wt, shell.Result{Code: 1, Stderr: "fatal: busy"})

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "removing the worktree failed") ||
		!strings.Contains(err.Error(), "rerun f10 finish GH-1") {
		t.Fatalf("err = %v", err)
	}

	if !fx.f.Called("herdr workspace close ws:9") {
		t.Errorf("the workspace did not close: %v", fx.f.Calls)
	}

	fx.f.Calls = nil
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.f.Script("git worktree remove "+fx.wt, "")

	if out, err := fx.run(t); err != nil {
		t.Fatalf("rerun: %v\n%s", err, out)
	}

	if fx.f.Called("gh pr merge") || !fx.f.Called("git worktree remove "+fx.wt) || !fx.f.Called("git branch -D GH-1") {
		t.Errorf("rerun calls: %v", fx.f.Calls)
	}
}

// A step that fails before the removal keeps the worktree and branch, so the
// rerun finds the task and redoes it.
func TestFinishKeepsTheWorktreeWhenAStepFails(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.dependent()
	fx.f.Meta.FailWrite("GH-2", branchmeta.Landed, errors.New("fatal: bad config"))

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "releasing dependents failed") ||
		!strings.Contains(err.Error(), "the worktree and branch stay for the rerun") {
		t.Fatalf("err = %v", err)
	}

	for _, p := range []string{"git worktree remove", "git branch -D", "herdr workspace close"} {
		if fx.f.Called(p) {
			t.Errorf("%q ran although a step failed", p)
		}
	}

	fx.f.Calls = nil
	fx.f.Script(testkit.GhView, testkit.PrMerged)

	if out, err := fx.run(t); err != nil {
		t.Fatalf("rerun: %v\n%s", err, out)
	}

	if !fx.f.Called("git worktree remove "+fx.wt) || !fx.f.Called("herdr workspace close ws:9") {
		t.Errorf("rerun calls: %v", fx.f.Calls)
	}
}

// A bare finish on a detached HEAD has no branch to name a task, so it
// refuses rather than finishing a branch named HEAD.
func TestFinishRefOnADetachedHead(t *testing.T) {
	repo := testkit.TracedRepo(t, "DM-5/fix", "")
	testkit.Git(t, repo, "checkout", "-q", "--detach")

	tg, err := target.At(t.Context(), repo, false, "finish")
	if err != nil {
		t.Fatal(err)
	}

	if r, _, branch, err := Ref(t.Context(), tg, ""); err == nil || branch == "HEAD" {
		t.Errorf("detached = %q on %q, %v; want a refusal", r.ID, branch, err)
	}
}

// reportLabels is the facts block's labels in order.
func reportLabels(out string) []string {
	var labels []string

	for _, m := range regexp.MustCompile(`(?m)^([a-z]+):\s`).FindAllStringSubmatch(out, -1) {
		labels = append(labels, m[1])
	}

	return labels
}

// Each way a finish lands prints its facts in one order, whichever path ran.
func TestFinishReportRowOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(fx *finishFixture)
		want  string
	}{
		"pull": {func(fx *finishFixture) { fx.openPR() }, "task pr merge plan workspace path"},
		"gone": {func(fx *finishFixture) {
			fx.f.Script(testkit.Worktrees, testkit.Porcelain())
			fx.f.Script(testkit.GhView, testkit.PrMerged)
			fx.f.Script(testkit.LsRem, "")
		}, "task pr merge plan"},
		"left open": {func(fx *finishFixture) {
			fx.f.Script(testkit.GhView, prOpenBy("alice"))
			fx.f.Script(unpushed, "")
			fx.in.Flags.LeaveOpen = true
		}, "task pr plan workspace path"},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.plan(t, "# plan")
			tc.setup(fx)

			out, err := fx.run(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}

			if got := strings.Join(reportLabels(out), " "); got != tc.want {
				t.Errorf("labels %q, want %q:\n%s", got, tc.want, out)
			}
		})
	}
}

// A rerun with the worktree gone sweeps as a whole finish does.
func TestFinishMergedWithoutWorktreeSweeps(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.Worktrees, testkit.Porcelain())
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.f.Script(testkit.LsRem, "")
	fx.f.Script("sweep", "")
	fx.in.Sweep = func(context.Context) { fx.f.Answer("sweep") }

	if out, err := fx.run(t); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	removed := slices.Index(fx.f.Calls, "git branch -D GH-1")
	if swept := slices.Index(fx.f.Calls, "sweep"); removed < 0 || swept < removed {
		t.Errorf("branch removed at %d, swept at %d", removed, swept)
	}
}
