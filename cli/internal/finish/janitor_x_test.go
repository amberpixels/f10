package finish_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/finish/finishtest"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A merged request whose head is the branch's tip, on a branch no worktree
// holds: both copies go, origin's through the API, and the plan is archived.
func TestJanitorRemovesAMergedBranch(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	plan := fx.Plan(t)
	reviews := filepath.Join(fx.In.Main, ".f10", "reviews")
	testkit.WriteFile(t, filepath.Join(reviews, "GH-1", "1.md"), "task: GH-1\nround: 1\n")
	handoff := archive.TaskHandoff(fx.In.Storage, "GH-1")
	testkit.WriteFile(t, handoff, "---\nhead: abc\n---\nNothing to do after merge.\n")

	out := fx.Run(t)

	if !fsx.Exists(filepath.Join(reviews, "archive", "GH-1.1", "1.md")) {
		t.Errorf("findings not archived:\n%s", out)
	}

	if fsx.Exists(handoff) || !fsx.Exists(filepath.Join(filepath.Dir(handoff), "archive", "GH-1.md")) {
		t.Errorf("handoff not archived:\n%s", out)
	}

	for _, call := range []string{
		"gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1/x",
		"git update-ref -d refs/remotes/origin/GH-1/x",
		"git branch -D GH-1/x",
	} {
		if !fx.F.Called(call) {
			t.Errorf("missing %q in %q", call, fx.F.Calls)
		}
	}

	if fsx.Exists(plan) || !fsx.Exists(filepath.Join(fx.In.Storage.Plans(), "archive", "GH-1.md")) {
		t.Errorf("plan not archived:\n%s", out)
	}

	if !strings.Contains(out, "removed GH-1/x (https://gh/pr/9 merged): local branch, origin") {
		t.Errorf("out = %q", out)
	}
}

// A merged branch's dependents are released, as finish does.
func TestJanitorReleasesDependents(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	fx.F.Meta.Seed("GH-2", branchmeta.AfterBranch, "GH-1/x")
	fx.F.Meta.Seed("GH-2", branchmeta.After, "GH-1")

	out := fx.Run(t)

	if !fx.F.Called("set branch.GH-2.f10-landed GH-1") || !fx.F.Called("git branch -D GH-1/x") {
		t.Errorf("dependent not released: %q\n%s", fx.F.Calls, out)
	}
}

// A branch a worktree holds is never asked about.
func TestJanitorSkipsAHeldBranch(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.F.Script(testkit.Worktrees, testkit.Porcelain("GH-1/x", "/code/repo.GH-1"))

	if out := fx.Run(t); out != "nothing to clear" || fx.F.Called("gh pr list --head") {
		t.Errorf("out = %q, calls %q", out, fx.F.Calls)
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
		fx := finishtest.NewJanitorFixture(t)
		fx.Branches(t, [3]string{"GH-1/x", c.tips[0], c.tips[1]})
		fx.Request("GH-1/x", "MERGED", "abc", "me")
		fx.F.Script(cherry("new"), c.lost)
		fx.Plan(t)

		out := fx.Run(t)

		if fx.F.Called("git branch -D") || fx.F.Called("gh api -X DELETE") ||
			!strings.Contains(out, "kept GH-1/x: "+c.wants) {
			t.Errorf("tips %v: out = %q", c.tips, out)
		}

		if !fsx.Exists(filepath.Join(fx.In.Storage.Plans(), "GH-1.md")) {
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
		fx := finishtest.NewJanitorFixture(t)
		fx.Branches(t, [3]string{"GH-1/x", tips[0], tips[1]})
		fx.Request("GH-1/x", "MERGED", "abc", "me")
		fx.F.Script(cherry("new"), "- 111 merged in")

		out := fx.Run(t)

		if !fx.F.Called("git branch -D GH-1/x") || !fx.F.Called("gh api -X DELETE") ||
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

	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "old", "old"})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	fx.F.Answers[cherry("old")] = []shell.Result{unknown, {}}
	fx.F.Script("git fetch --quiet origin abc", "")

	out := fx.Run(t)

	if !fx.F.Called("git fetch --quiet origin abc") || !strings.Contains(out, "removed GH-1/x") {
		t.Errorf("out = %q", out)
	}

	fx = finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "old", ""})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	fx.F.Fail(cherry("old"), unknown)
	fx.F.Fail("git fetch --quiet origin abc", shell.Result{Code: 128, Stderr: "fatal: couldn't find remote ref abc"})

	out = fx.Run(t)

	if fx.F.Called("git branch -D") ||
		!strings.Contains(
			out,
			"kept GH-1/x: reading what its tip has beyond https://gh/pr/9's head failed: fetching its head abc: ",
		) {
		t.Errorf("out = %q", out)
	}
}

// An open request based on the branch would be closed by the delete.
func TestJanitorKeepsAStackedBase(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	fx.F.Script(testkit.GhStackOf("GH-1/x"), `[{"number":12,"url":"https://gh/pr/12","headRefName":"GH-2"}]`)

	out := fx.Run(t)

	if fx.F.Called("git branch -D") || !strings.Contains(out, "https://gh/pr/12 is based on it") {
		t.Errorf("out = %q", out)
	}
}

// An open request, even past a newer closed one, keeps the branch without a line.
func TestJanitorKeepsAnOpenRequest(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.F.Script(
		testkit.GhHeadOf("GH-1/x"),
		`[{"number":9,"state":"CLOSED","headRefOid":"abc","createdAt":"2026-10-05T00:00:00Z"},`+
			`{"number":8,"state":"OPEN","headRefOid":"abc","createdAt":"2026-10-01T00:00:00Z"}]`,
	)

	if out := fx.Run(t); out != "nothing to clear" || fx.F.Called("git branch -D") {
		t.Errorf("out = %q", out)
	}
}

// A closed request is an abandoned task, unless another task still builds
// on the branch.
func TestJanitorClosedRequest(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.Request("GH-1/x", "CLOSED", "abc", "me")
	fx.F.Meta.Seed("GH-2", branchmeta.AfterBranch, "GH-1/x")

	if out := fx.Run(t); fx.F.Called("git branch -D") || !strings.Contains(out, "GH-2 started --after it") {
		t.Errorf("with a dependent: out = %q", out)
	}

	fx = finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.Request("GH-1/x", "CLOSED", "abc", "me")

	if out := fx.Run(t); !fx.F.Called("git branch -D GH-1/x") {
		t.Errorf("without one: out = %q", out)
	}
}

// Someone else's request loses only the local branch; a branch of theirs
// that is only on origin is not touched at all.
func TestJanitorKeepsAnotherAuthorsOrigin(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"}, [3]string{"GH-2/y", "", "def"})
	fx.Request("GH-1/x", "MERGED", "abc", "bob")
	fx.Request("GH-2/y", "MERGED", "def", "bob")

	out := fx.Run(t)

	if fx.F.Called("gh api -X DELETE") || fx.F.Called("git update-ref -d") || !fx.F.Called("git branch -D GH-1/x") {
		t.Errorf("calls %q", fx.F.Calls)
	}

	if !strings.Contains(out, "kept origin/GH-1/x: https://gh/pr/9 was opened by bob") ||
		strings.Contains(out, "GH-2/y") {
		t.Errorf("out = %q", out)
	}
}

// A branch tied to no task, and the default branch, are never touched.
func TestJanitorIgnoresBranchesWithoutATask(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.F.Script(testkit.AllHeads, testkit.Heads("main", "release/1.2"))
	fx.F.Script(testkit.LsHeads, "abc\trefs/heads/main\nabc\trefs/heads/dependabot/x")

	if out := fx.Run(t); out != "nothing to clear" || fx.F.Called("gh ") {
		t.Errorf("out = %q, calls %q", out, fx.F.Calls)
	}
}

// Without a host, only a branch never pushed whose changes are in the
// default branch goes.
func TestJanitorWithoutAHost(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.In.Host = nil
	fx.Branches(t, [3]string{"GH-1/x", "abc", ""}, [3]string{"GH-2/y", "def", ""}, [3]string{"GH-3/z", "ghi", "ghi"})
	fx.F.Script("git config --get branch.GH-1/x.merge", "")
	fx.F.Script("git config --get branch.GH-2/y.merge", "")
	fx.F.Script("git merge-tree --write-tree refs/heads/main refs/heads/GH-1/x", "t1")
	fx.F.Script("git merge-tree --write-tree refs/heads/main refs/heads/GH-2/y", "t2")
	fx.F.Script("git rev-parse refs/heads/main^{tree}", "t1")
	fx.F.Script("git log -1 --format=%cI abc", "2026-10-01T00:00:00Z")

	out := fx.Run(t)

	if !fx.F.Called("git branch -D GH-1/x") || fx.F.Called("git branch -D GH-2/y") ||
		fx.F.Called("git branch -D GH-3/z") {
		t.Errorf("calls %q", fx.F.Calls)
	}

	if !strings.Contains(out, "removed GH-1/x (never pushed, already in main): local branch") {
		t.Errorf("out = %q", out)
	}
}

// A dry run says what would go and changes nothing.
func TestJanitorDryRun(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.In.Dry = true
	fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	plan := fx.Plan(t)
	reviews := filepath.Join(fx.In.Main, ".f10", "reviews", "GH-1")
	testkit.WriteFile(t, filepath.Join(reviews, "1.md"), "task: GH-1\nround: 1\n")
	handoff := archive.TaskHandoff(fx.In.Storage, "GH-1")
	testkit.WriteFile(t, handoff, "---\nhead: abc\n---\nNothing to do after merge.\n")

	out := fx.Run(t)

	if !fsx.Exists(reviews) || !strings.Contains(out, "would archive "+reviews) {
		t.Errorf("dry run moved or left out the findings:\n%s", out)
	}

	if !fsx.Exists(handoff) || !strings.Contains(out, "would archive "+handoff) {
		t.Errorf("dry run moved or left out the handoff:\n%s", out)
	}

	if fx.F.Called("git branch -D") || fx.F.Called("gh api -X DELETE") || fx.F.Called("git update-ref") ||
		!fsx.Exists(plan) {
		t.Errorf("dry run changed something: %q", fx.F.Calls)
	}

	for _, want := range []string{"would remove GH-1/x (https://gh/pr/9 merged): local branch, origin", "would archive " + plan} {
		if !strings.Contains(out, want) {
			t.Errorf("out = %q, want %q", out, want)
		}
	}
}

// A task with a second branch still in a worktree keeps its plan.
func TestJanitorKeepsThePlanOfATaskStillOpen(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", ""}, [3]string{"GH-1-2", "def", ""})
	fx.F.Script(testkit.Worktrees, testkit.Porcelain("GH-1-2", "/code/repo.GH-1-2"))
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	plan := fx.Plan(t)

	out := fx.Run(t)

	if !fx.F.Called("git branch -D GH-1/x") || !fsx.Exists(plan) || !strings.Contains(out, "GH-1-2 still uses it") {
		t.Errorf("out = %q", out)
	}
}

// A plan written after the request ended belongs to a new attempt at the
// task, and stays.
func TestJanitorKeepsAPlanWrittenAfterTheRequestEnded(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.F.Script(testkit.GhHeadOf("GH-1/x"), `[{"number":9,"state":"CLOSED","url":"https://gh/pr/9","headRefOid":"abc",`+
		`"closedAt":"2026-01-01T00:00:00Z","author":{"login":"me"}}]`)
	plan := fx.Plan(t)

	images := filepath.Join(fx.In.Main, ".f10", "images", "GH-1")
	if err := os.MkdirAll(images, 0o755); err != nil {
		t.Fatal(err)
	}

	out := fx.Run(t)

	if !fx.F.Called("git branch -D GH-1/x") || !fsx.Exists(plan) || !strings.Contains(out, "written after") {
		t.Errorf("out = %q", out)
	}

	if !fsx.Exists(images) {
		t.Error("the kept plan lost its images")
	}
}

// A bundle the config records, not the name, still archives every member.
func TestJanitorReadsRecordedRiders(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	fx.F.Meta.Seed("GH-1/x", branchmeta.Task, "GH-1")
	fx.F.Meta.Seed("GH-1/x", branchmeta.Riders, "GH-2")

	rider := filepath.Join(fx.In.Storage.Plans(), "GH-2.md")
	if err := os.MkdirAll(fx.In.Storage.Plans(), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(rider, []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out := fx.Run(t); fsx.Exists(rider) {
		t.Errorf("rider's plan not archived:\n%s", out)
	}
}

// A branch whose name carries no id is still its linked task's: a merged
// picked-up PR's branch goes, and that task's plan with it.
func TestJanitorReadsALinkWithoutRiders(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"alice/fix", "abc", ""})
	fx.Request("alice/fix", "MERGED", "abc", "me")
	fx.F.Meta.Seed("alice/fix", branchmeta.Task, "GH-1")
	plan := fx.Plan(t)

	out := fx.Run(t)

	if !fx.F.Called("git branch -D alice/fix") || fsx.Exists(plan) {
		t.Errorf("a link-only branch was not cleared:\n%s\ncalls: %q", out, fx.F.Calls)
	}
}

// A bundle branch ships every member: its riders' plans go with the lead's.
func TestJanitorArchivesARidersPlan(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1+GH-2/x", "abc", ""})
	fx.Request("GH-1+GH-2/x", "MERGED", "abc", "me")
	fx.Plan(t)

	rider := filepath.Join(fx.In.Storage.Plans(), "GH-2.md")
	if err := os.WriteFile(rider, []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := fx.Run(t)

	if fsx.Exists(rider) || !fsx.Exists(filepath.Join(fx.In.Storage.Plans(), "archive", "GH-2.md")) {
		t.Errorf("rider's plan not archived:\n%s", out)
	}
}

// On origin only the shapes start creates count; a name merely mentioning
// an id is someone else's branch.
func TestJanitorReadsOriginByStartsShapes(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.F.Script(testkit.AllHeads, testkit.Heads("main"))
	fx.F.Script(testkit.LsHeads, "abc\trefs/heads/revert-GH-1\nabc\trefs/heads/feat/GH-2-x")

	if out := fx.Run(t); out != "nothing to clear" || fx.F.Called("gh ") || len(fx.F.Meta.Writes()) > 0 {
		t.Errorf("out = %q, calls %q", out, fx.F.Calls)
	}
}

// With a host, a branch with no request is removed only as never pushed.
func TestJanitorRemovesALandedBranchWithNoRequest(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", ""})
	fx.F.Script(testkit.GhHeadOf("GH-1/x"), "[]")
	fx.F.Script("git config --get branch.GH-1/x.merge", "")
	fx.F.Script("git merge-tree --write-tree refs/heads/main refs/heads/GH-1/x", "t1")
	fx.F.Script("git rev-parse refs/heads/main^{tree}", "t1")
	fx.F.Script("git log -1 --format=%cI abc", "2026-10-01T00:00:00Z")

	if out := fx.Run(t); !strings.Contains(out, "removed GH-1/x (never pushed, already in main)") {
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
		fx := finishtest.NewJanitorFixture(t)
		fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"}, [3]string{"GH-2/y", "def", ""})
		fx.Request("GH-1/x", "MERGED", "abc", "me")
		fx.Request("GH-2/y", "MERGED", "def", "me")
		fx.F.Fail(c.fail, shell.Result{Code: 1, Stderr: "refused"})
		plan := fx.Plan(t)

		out := fx.Run(t)

		if !strings.Contains(out, "kept GH-1/x: "+c.want) || !fsx.Exists(plan) || !fx.F.Called("git branch -D GH-2/y") {
			t.Errorf("%s: out = %q", c.fail, out)
		}
	}
}

// A local tip git fails to read is not a branch with no local copy: the
// branch stays for this pass, origin's copy included.
func TestJanitorKeepsABranchWhoseTipItCannotRead(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	fx.F.Fail("git rev-parse -q --verify refs/heads/GH-1/x", shell.Result{Code: 128, Stderr: "fatal: bad object"})

	out := fx.Run(t)

	if fx.F.Called("gh api -X DELETE repos/{owner}/{repo}/git/refs/heads/GH-1/x") ||
		fx.F.Called("git branch -D GH-1/x") {
		t.Errorf("deleted a branch whose tip it could not read:\n%s", out)
	}

	if !strings.Contains(out, "kept GH-1/x") {
		t.Errorf("out = %q, want the branch kept with the reason", out)
	}
}

// A plan that cannot move is a line and keeps its images, and the task's
// findings and handoff still go: the janitor reaches the task only through the branch it just
// removed, so nothing would come back for them.
func TestJanitorArchivesTheRestWhenThePlanCannotMove(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	fx.Branches(t, [3]string{"GH-1/x", "abc", "abc"})
	fx.Request("GH-1/x", "MERGED", "abc", "me")
	plan := fx.Plan(t)
	testkit.WriteFile(t, filepath.Join(fx.In.Storage.Plans(), "archive"), "")
	reviews := filepath.Join(fx.In.Main, ".f10", "reviews")
	testkit.WriteFile(t, filepath.Join(reviews, "GH-1", "1.md"), "task: GH-1\nround: 1\n")
	handoff := archive.TaskHandoff(fx.In.Storage, "GH-1")
	testkit.WriteFile(t, handoff, "---\nhead: abc\n---\nNothing to do after merge.\n")
	image := filepath.Join(fx.In.Storage.Images("GH-1"), "1.png")
	testkit.WriteFile(t, image, "png")

	out := fx.Run(t)

	if !fsx.Exists(plan) || !fsx.Exists(image) || !strings.Contains(out, "archiving the plan at "+plan) {
		t.Errorf("want the plan and its images kept and the failure named:\n%s", out)
	}

	if !fsx.Exists(filepath.Join(reviews, "archive", "GH-1.1", "1.md")) || fsx.Exists(handoff) {
		t.Errorf("findings or handoff not archived:\n%s", out)
	}
}
