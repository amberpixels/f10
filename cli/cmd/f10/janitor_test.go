package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// The fixture every janitor test starts from: origin's HEAD is main, the
// main checkout is on main and nothing else holds a branch, and no branch
// records a dependent. Each test scripts its branches and requests.
type janitorFixture struct {
	f  *fakes
	in janitorInput
}

const (
	lsHeads   = "git ls-remote --heads origin"
	originURL = "git remote get-url origin"
	ghLogin   = "gh api user"
)

func newJanitorFixture(t *testing.T) *janitorFixture {
	t.Helper()

	main := t.TempDir()
	fx := &janitorFixture{
		f:  newFakes(t),
		in: janitorInput{main: main, prefix: "GH", plansDir: filepath.Join(main, ".f10", "plans")},
	}

	h := newHost("gh", main)
	fx.in.host = &h

	f := fx.f
	f.script(headRef, "origin/main")
	f.script(originURL, "git@x:a/b.git")
	f.script(worktrees, porcelain())
	f.script(cfgDeps, "")
	f.script(ghLogin, `{"login":"me"}`)

	return fx
}

// branch scripts a local branch at sha ("" for none) and origin's copy at
// originSHA ("" for none); call it once per test with every branch.
func (fx *janitorFixture) branches(t *testing.T, bs ...[3]string) {
	t.Helper()

	var local, remote []string

	for _, b := range bs {
		if b[1] != "" {
			local = append(local, b[0])
			fx.f.script("git rev-parse -q --verify refs/heads/"+b[0], b[1])
			fx.f.script("git branch -D "+b[0], "")
		}

		if b[2] != "" {
			remote = append(remote, b[2]+"\trefs/heads/"+b[0])
			fx.f.script(ghStackOf(b[0]), "[]")
			fx.f.script("gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/"+b[0], "")
			fx.f.script("git update-ref -d refs/remotes/origin/"+b[0], "")
		}

		fx.f.script(ghStackOf(b[0]), "[]")
	}

	fx.f.script(allHeads, heads(append(local, "main")...))
	fx.f.script(lsHeads, strings.Join(remote, "\n"))
}

func ghHeadOf(branch string) string {
	return "gh pr list --head " + branch + " --state all --json " + forge.HeadFields + " --limit 100"
}

func ghStackOf(branch string) string {
	return "gh pr list --base " + branch + " --state open --limit 1000 --json number,url,headRefName"
}

func (fx *janitorFixture) request(branch, state, sha, author string) {
	fx.f.script(ghHeadOf(branch), `[{"number":9,"state":"`+state+`","url":"https://gh/pr/9","headRefName":"`+
		branch+`","headRefOid":"`+sha+`","author":{"login":"`+author+`"}}]`)
}

func (fx *janitorFixture) plan(t *testing.T) string {
	t.Helper()

	p := filepath.Join(fx.in.plansDir, "GH-1.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	return p
}

func (fx *janitorFixture) run(t *testing.T) string {
	t.Helper()

	lines, err := janitor(t.Context(), fx.in)
	if err != nil {
		t.Fatal(err)
	}

	return strings.Join(lines, "\n")
}

// A merged request whose head is the branch's tip, on a branch no worktree
// holds: both copies go, origin's through the API, and the plan is archived.
func TestJanitorRemovesAMergedBranch(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.request("GH-1/x", "MERGED", "abc", "me")
	plan := fx.plan(t)

	out := fx.run(t)

	for _, call := range []string{
		"gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1/x",
		"git update-ref -d refs/remotes/origin/GH-1/x",
		"git branch -D GH-1/x",
	} {
		if !fx.f.called(call) {
			t.Errorf("missing %q in %q", call, fx.f.calls)
		}
	}

	if exists(plan) || !exists(filepath.Join(fx.in.plansDir, "archive", "GH-1.md")) {
		t.Errorf("plan not archived:\n%s", out)
	}

	if !strings.Contains(out, "removed GH-1/x (https://gh/pr/9 merged): local branch, origin") {
		t.Errorf("out = %q", out)
	}
}

// A merged branch's dependents are released, as finish does.
func TestJanitorReleasesDependents(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.request("GH-1/x", "MERGED", "abc", "me")
	fx.f.script(cfgDeps, "branch.GH-2.f10-after-branch GH-1/x")
	fx.f.script("git config --get branch.GH-2.f10-after", "GH-1")
	fx.f.script("git config --unset branch.GH-2.f10-after", "")
	fx.f.script("git config --unset branch.GH-2.f10-after-branch", "")
	fx.f.script("git config branch.GH-2.f10-landed GH-1", "")

	out := fx.run(t)

	if !fx.f.called("git config branch.GH-2.f10-landed GH-1") || !fx.f.called("git branch -D GH-1/x") {
		t.Errorf("dependent not released: %q\n%s", fx.f.calls, out)
	}
}

// A branch a worktree holds is never asked about.
func TestJanitorSkipsAHeldBranch(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.f.script(worktrees, porcelain("GH-1/x", "/code/repo.GH-1"))

	if out := fx.run(t); out != "nothing to clear" || fx.f.called("gh pr list --head") {
		t.Errorf("out = %q, calls %q", out, fx.f.calls)
	}
}

// Commits past the request's head, locally or on origin, keep the branch.
// A tip past the request's head with a commit main lacks by patch: the
// merge left it behind, and the line names it, whichever copy has it.
func TestJanitorKeepsABranchAheadOfItsRequest(t *testing.T) {
	for _, c := range []struct {
		tips  [2]string
		lost  string
		wants string
	}{
		{[2]string{"new", "abc"}, "+ f00ba4deadbeef address ai feedback", `its "address ai feedback" (f00ba4d) never reached https://gh/pr/9`},
		{[2]string{"abc", "new"}, "+ f00ba4deadbeef fix", `origin's "fix" (f00ba4d) never reached https://gh/pr/9`},
		{[2]string{"new", "new"}, "- 111 merged\n+ 222 one\n+ 333 two", `its "one" (222) and 1 more commit never reached`},
		{[2]string{"new", ""}, "+ 1 a\n+ 2 b\n+ 3 c", `its "a" (1) and 2 more commits never reached`},
	} {
		fx := newJanitorFixture(t)
		fx.branches(t, [3]string{"GH-1/x", c.tips[0], c.tips[1]})
		fx.request("GH-1/x", "MERGED", "abc", "me")
		fx.f.script(cherry("new"), c.lost)
		fx.plan(t)

		out := fx.run(t)

		if fx.f.called("git branch -D") || fx.f.called("gh api -X DELETE") ||
			!strings.Contains(out, "kept GH-1/x: "+c.wants) {
			t.Errorf("tips %v: out = %q", c.tips, out)
		}

		if !exists(filepath.Join(fx.in.plansDir, "GH-1.md")) {
			t.Errorf("tips %v: plan moved", c.tips)
		}
	}
}

func cherry(tip string) string {
	return "git cherry -v refs/heads/main " + tip + " abc"
}

// A tip past the request's head whose every commit main has by patch (a
// catchup never pushed, a follow-up that landed another way) lost nothing
// at the merge, and so goes.
func TestJanitorRemovesABranchWhoseExtraCommitsLanded(t *testing.T) {
	for _, tips := range [][2]string{{"new", "abc"}, {"abc", "new"}, {"new", "new"}} {
		fx := newJanitorFixture(t)
		fx.branches(t, [3]string{"GH-1/x", tips[0], tips[1]})
		fx.request("GH-1/x", "MERGED", "abc", "me")
		fx.f.script(cherry("new"), "- 111 merged in")

		out := fx.run(t)

		if !fx.f.called("git branch -D GH-1/x") || !fx.f.called("gh api -X DELETE") ||
			!strings.Contains(out, "removed GH-1/x") {
			t.Errorf("tips %v: out = %q", tips, out)
		}
	}
}

// A head the clone never fetched (a colleague's push, the branch deleted on
// origin since) is fetched by sha and then read; a fetch that fails keeps
// the branch with a line.
func TestJanitorFetchesARequestHeadTheCloneLacks(t *testing.T) {
	unknown := shell.Result{Code: 128, Stderr: "fatal: unknown commit abc"}

	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "old", "old"})
	fx.request("GH-1/x", "MERGED", "abc", "me")
	fx.f.answers[cherry("old")] = []shell.Result{unknown, {}}
	fx.f.script("git fetch --quiet origin abc", "")

	out := fx.run(t)

	if !fx.f.called("git fetch --quiet origin abc") || !strings.Contains(out, "removed GH-1/x") {
		t.Errorf("out = %q", out)
	}

	fx = newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "old", ""})
	fx.request("GH-1/x", "MERGED", "abc", "me")
	fx.f.fail(cherry("old"), unknown)
	fx.f.fail("git fetch --quiet origin abc", shell.Result{Code: 128, Stderr: "fatal: couldn't find remote ref abc"})

	out = fx.run(t)

	if fx.f.called("git branch -D") ||
		!strings.Contains(
			out,
			"kept GH-1/x: reading what its tip has beyond https://gh/pr/9's head failed: fetching its head abc: ",
		) {
		t.Errorf("out = %q", out)
	}
}

// An open request based on the branch would be closed by the delete.
func TestJanitorKeepsAStackedBase(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.request("GH-1/x", "MERGED", "abc", "me")
	fx.f.script(ghStackOf("GH-1/x"), `[{"number":12,"url":"https://gh/pr/12","headRefName":"GH-2"}]`)

	out := fx.run(t)

	if fx.f.called("git branch -D") || !strings.Contains(out, "https://gh/pr/12 is based on it") {
		t.Errorf("out = %q", out)
	}
}

// An open request, even past a newer closed one, keeps the branch quietly.
func TestJanitorKeepsAnOpenRequest(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.f.script(
		ghHeadOf("GH-1/x"),
		`[{"number":9,"state":"CLOSED","headRefOid":"abc","createdAt":"2026-10-05T00:00:00Z"},`+
			`{"number":8,"state":"OPEN","headRefOid":"abc","createdAt":"2026-10-01T00:00:00Z"}]`,
	)

	if out := fx.run(t); out != "nothing to clear" || fx.f.called("git branch -D") {
		t.Errorf("out = %q", out)
	}
}

// A closed request is an abandoned task, unless another task still builds
// on the branch.
func TestJanitorClosedRequest(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.request("GH-1/x", "CLOSED", "abc", "me")
	fx.f.script(cfgDeps, "branch.GH-2.f10-after-branch GH-1/x")

	if out := fx.run(t); fx.f.called("git branch -D") || !strings.Contains(out, "GH-2 started --after it") {
		t.Errorf("with a dependent: out = %q", out)
	}

	fx = newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.request("GH-1/x", "CLOSED", "abc", "me")

	if out := fx.run(t); !fx.f.called("git branch -D GH-1/x") {
		t.Errorf("without one: out = %q", out)
	}
}

// Someone else's request loses only the local branch; a branch of theirs
// that is only on origin is not touched at all.
func TestJanitorKeepsAnotherAuthorsOrigin(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", "abc"}, [3]string{"GH-2/y", "", "def"})
	fx.request("GH-1/x", "MERGED", "abc", "bob")
	fx.request("GH-2/y", "MERGED", "def", "bob")

	out := fx.run(t)

	if fx.f.called("gh api -X DELETE") || fx.f.called("git update-ref -d") || !fx.f.called("git branch -D GH-1/x") {
		t.Errorf("calls %q", fx.f.calls)
	}

	if !strings.Contains(out, "kept origin/GH-1/x: https://gh/pr/9 was opened by bob") ||
		strings.Contains(out, "GH-2/y") {
		t.Errorf("out = %q", out)
	}
}

// A branch tied to no task, and the default branch, are never touched.
func TestJanitorIgnoresBranchesWithoutATask(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.f.script(allHeads, heads("main", "release/1.2"))
	fx.f.script(lsHeads, "abc\trefs/heads/main\nabc\trefs/heads/dependabot/x")

	if out := fx.run(t); out != "nothing to clear" || fx.f.called("gh ") {
		t.Errorf("out = %q, calls %q", out, fx.f.calls)
	}
}

// Without a host, only a branch never pushed whose changes are in the
// default branch goes.
func TestJanitorWithoutAHost(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.in.host = nil
	fx.branches(t, [3]string{"GH-1/x", "abc", ""}, [3]string{"GH-2/y", "def", ""}, [3]string{"GH-3/z", "ghi", "ghi"})
	fx.f.script("git config --get branch.GH-1/x.merge", "")
	fx.f.script("git config --get branch.GH-2/y.merge", "")
	fx.f.script("git merge-tree --write-tree refs/heads/main refs/heads/GH-1/x", "t1")
	fx.f.script("git merge-tree --write-tree refs/heads/main refs/heads/GH-2/y", "t2")
	fx.f.script("git rev-parse refs/heads/main^{tree}", "t1")
	fx.f.script("git log -1 --format=%cI abc", "2026-10-01T00:00:00Z")

	out := fx.run(t)

	if !fx.f.called("git branch -D GH-1/x") || fx.f.called("git branch -D GH-2/y") ||
		fx.f.called("git branch -D GH-3/z") {
		t.Errorf("calls %q", fx.f.calls)
	}

	if !strings.Contains(out, "removed GH-1/x (never pushed, already in main): local branch") {
		t.Errorf("out = %q", out)
	}
}

// A dry run says what would go and changes nothing.
func TestJanitorDryRun(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.in.dry = true
	fx.branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.request("GH-1/x", "MERGED", "abc", "me")
	plan := fx.plan(t)

	out := fx.run(t)

	if fx.f.called("git branch -D") || fx.f.called("gh api -X DELETE") || fx.f.called("git update-ref") ||
		!exists(plan) {
		t.Errorf("dry run changed something: %q", fx.f.calls)
	}

	for _, want := range []string{"would remove GH-1/x (https://gh/pr/9 merged): local branch, origin", "would archive " + plan} {
		if !strings.Contains(out, want) {
			t.Errorf("out = %q, want %q", out, want)
		}
	}
}

// A task with a second branch still in a worktree keeps its plan.
func TestJanitorKeepsThePlanOfATaskStillOpen(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", ""}, [3]string{"GH-1-2", "def", ""})
	fx.f.script(worktrees, porcelain("GH-1-2", "/code/repo.GH-1-2"))
	fx.request("GH-1/x", "MERGED", "abc", "me")
	plan := fx.plan(t)

	out := fx.run(t)

	if !fx.f.called("git branch -D GH-1/x") || !exists(plan) || !strings.Contains(out, "GH-1-2 still uses it") {
		t.Errorf("out = %q", out)
	}
}

func TestJanitorDue(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), janitorStamp)
	now := time.Now()

	if !janitorDue(stamp, now) {
		t.Error("no stamp: not due")
	}

	if err := touchStamp(stamp, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	if janitorDue(stamp, now) {
		t.Error("an hour-old stamp: due")
	}

	if !janitorDue(stamp, now.Add(janitorEvery)) {
		t.Error("a stamp past the interval: not due")
	}
}

// After start or finish, a fresh stamp skips the janitor; a due one runs it, and its
// failure is a warning with the stamp moved, never an error.
func TestSweepIfDue(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "f10", janitorStamp)
	now := time.Now()

	var runs int

	run := func(context.Context) ([]string, error) {
		runs++

		return []string{"removed GH-1/x"}, errors.New("gh did not answer")
	}

	var out bytes.Buffer

	sweepIfDue(t.Context(), &out, stamp, now, run)

	if runs != 1 || janitorDue(stamp, now) {
		t.Fatalf("runs = %d, due after the run = %v", runs, janitorDue(stamp, now))
	}

	want := []string{"janitor: removed GH-1/x", "janitor: warning: gh did not answer"}
	if got := strings.Split(strings.TrimSpace(out.String()), "\n"); !slices.Equal(got, want) {
		t.Errorf("out = %q", got)
	}

	sweepIfDue(t.Context(), &out, stamp, now.Add(time.Minute), run)

	if runs != 1 {
		t.Error("a fresh stamp ran the janitor again")
	}
}

// A plan written after the request ended belongs to a new attempt at the
// task, and stays.
func TestJanitorKeepsAPlanWrittenAfterTheRequestEnded(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.f.script(ghHeadOf("GH-1/x"), `[{"number":9,"state":"CLOSED","url":"https://gh/pr/9","headRefOid":"abc",`+
		`"closedAt":"2026-01-01T00:00:00Z","author":{"login":"me"}}]`)
	plan := fx.plan(t)

	images := filepath.Join(fx.in.main, ".f10", "images", "GH-1")
	if err := os.MkdirAll(images, 0o755); err != nil {
		t.Fatal(err)
	}

	out := fx.run(t)

	if !fx.f.called("git branch -D GH-1/x") || !exists(plan) || !strings.Contains(out, "written after") {
		t.Errorf("out = %q", out)
	}

	if !exists(images) {
		t.Error("the kept plan lost its images")
	}
}

// A bundle the config records, not the name, still archives every member.
func TestJanitorReadsRecordedRiders(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.request("GH-1/x", "MERGED", "abc", "me")
	fx.f.script(linkedKeys, "branch.GH-1/x.f10-task GH-1\nbranch.GH-1/x.f10-tasks GH-2")

	rider := filepath.Join(fx.in.plansDir, "GH-2.md")
	if err := os.MkdirAll(fx.in.plansDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(rider, []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out := fx.run(t); exists(rider) {
		t.Errorf("rider's plan not archived:\n%s", out)
	}
}

// A bundle branch ships every member: its riders' plans go with the lead's.
func TestJanitorArchivesARidersPlan(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1+GH-2/x", "abc", ""})
	fx.request("GH-1+GH-2/x", "MERGED", "abc", "me")
	fx.plan(t)

	rider := filepath.Join(fx.in.plansDir, "GH-2.md")
	if err := os.WriteFile(rider, []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := fx.run(t)

	if exists(rider) || !exists(filepath.Join(fx.in.plansDir, "archive", "GH-2.md")) {
		t.Errorf("rider's plan not archived:\n%s", out)
	}
}

// On origin only the shapes start creates count; a name merely mentioning
// an id is someone else's branch.
func TestJanitorReadsOriginByStartsShapes(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.f.script(allHeads, heads("main"))
	fx.f.script(lsHeads, "abc\trefs/heads/revert-GH-1\nabc\trefs/heads/feat/GH-2-x")

	if out := fx.run(t); out != "nothing to clear" || fx.f.called("gh ") || fx.f.called("git config --get branch.") {
		t.Errorf("out = %q, calls %q", out, fx.f.calls)
	}
}

// With a host, a branch with no request is removed only as never pushed.
func TestJanitorRemovesALandedBranchWithNoRequest(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.f.script(ghHeadOf("GH-1/x"), "[]")
	fx.f.script("git config --get branch.GH-1/x.merge", "")
	fx.f.script("git merge-tree --write-tree refs/heads/main refs/heads/GH-1/x", "t1")
	fx.f.script("git rev-parse refs/heads/main^{tree}", "t1")
	fx.f.script("git log -1 --format=%cI abc", "2026-10-01T00:00:00Z")

	if out := fx.run(t); !strings.Contains(out, "removed GH-1/x (never pushed, already in main)") {
		t.Errorf("out = %q", out)
	}
}

// A step failing keeps that branch and its plan, says so, and the next
// branch is still swept.
func TestJanitorFailureKeepsTheBranch(t *testing.T) {
	for _, c := range []struct{ fail, want string }{
		{"gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1/x", "deleting origin/GH-1/x failed"},
		{"git branch -D GH-1/x", "deleting the local branch failed"},
	} {
		fx := newJanitorFixture(t)
		fx.branches(t, [3]string{"GH-1/x", "abc", "abc"}, [3]string{"GH-2/y", "def", ""})
		fx.request("GH-1/x", "MERGED", "abc", "me")
		fx.request("GH-2/y", "MERGED", "def", "me")
		fx.f.fail(c.fail, shell.Result{Code: 1, Stderr: "refused"})
		plan := fx.plan(t)

		out := fx.run(t)

		if !strings.Contains(out, "kept GH-1/x: "+c.want) || !exists(plan) || !fx.f.called("git branch -D GH-2/y") {
			t.Errorf("%s: out = %q", c.fail, out)
		}
	}
}

// The delete escapes the branch: GitHub keeps its slashes as segments,
// GitLab takes the whole name as one.
func TestDeleteRemoteBranchEscapesTheName(t *testing.T) {
	f := newFakes(t)
	f.script("gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1/a%23b", "")
	f.script("glab api -X DELETE projects/:fullpath/repository/branches/GH-1%2Fa%23b", "")

	for _, name := range []string{"gh", "glab"} {
		if err := newHost(name, "/code/repo").deleteRemoteBranch(t.Context(), "GH-1/a#b"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
