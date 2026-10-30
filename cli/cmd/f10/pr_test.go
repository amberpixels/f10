package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/probe"
)

// prRepo is a clone of a bare origin, so origin/HEAD and pushed branches
// exist; origin is a path, so no host CLI applies and every read is git's.
func prRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)

	dir := filepath.Join(root, "repo")
	git(t, root, "clone", "-q", origin, dir)
	git(t, dir, "config", "user.email", "me@x.io")
	git(t, dir, "config", "user.name", "me")
	commitFile(t, dir, "main.txt")
	git(t, dir, "push", "-q", "origin", "main")
	git(t, dir, "remote", "set-head", "origin", "-a")

	return dir
}

func commitFile(t *testing.T, dir, name string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", name)
}

// stacked puts feat on a pushed base branch, recorded as start records it.
func stacked(t *testing.T) string {
	t.Helper()

	dir := prRepo(t)
	git(t, dir, "switch", "-q", "-c", "base")
	commitFile(t, dir, "base.txt")
	git(t, dir, "push", "-q", "origin", "base")
	git(t, dir, "switch", "-q", "-c", "feat")
	commitFile(t, dir, "feat.txt")
	git(t, dir, "config", "branch.feat."+cfgAfterBranch, "base")

	return dir
}

func runPR(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &errOut

	err := app.Run(context.Background(), append([]string{"f10", "-C", dir, "pr"}, args...))

	return out.String(), errOut.String(), err
}

func TestPRBase(t *testing.T) {
	dir := stacked(t)

	if out, _, err := runPR(t, dir, "base"); err != nil || out != "base\n" {
		t.Errorf("stacked: base = %q, %v; want base", out, err)
	}

	git(t, dir, "config", "branch.feat."+cfgAfterBranch, "gone")

	out, errOut, err := runPR(t, dir, "base")
	if err != nil || out != "main\nfound: "+foundGone+"\n" || errOut != "" {
		t.Errorf("gone: base = %q, %q, %v; want main and the typed found line, nothing on stderr", out, errOut, err)
	}

	git(t, dir, "config", "--unset", "branch.feat."+cfgAfterBranch)

	if out, _, err := runPR(t, dir, "base"); err != nil || out != "main\n" {
		t.Errorf("unrecorded: base = %q, %v; want main", out, err)
	}
}

// A PR cannot target a base origin lacks, so --origin refuses it.
func TestPRBaseOriginRefusesALocalBase(t *testing.T) {
	dir := prRepo(t)
	git(t, dir, "switch", "-q", "-c", "base")
	commitFile(t, dir, "base.txt")
	git(t, dir, "switch", "-q", "-c", "feat")
	git(t, dir, "config", "branch.feat."+cfgAfterBranch, "base")

	if _, _, err := runPR(t, dir, "base", "--origin"); err == nil || !strings.Contains(err.Error(), "not on origin") {
		t.Errorf("err = %v, want the base named as not on origin", err)
	}
}

// A stacked branch's diff holds its own work, uncommitted included, and
// none of the base task's commits.
func TestPRDiffStacked(t *testing.T) {
	dir := stacked(t)

	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runPR(t, dir, "diff", "--name-only")
	if err != nil || out != "feat.txt\nmain.txt\n" {
		t.Errorf("diff = %q, %v; want feat.txt and the uncommitted main.txt", out, err)
	}
}

// The base's unpushed commit the branch was cut from is the base's work,
// and origin's copy of the base moving ahead is not this change either.
func TestPRDiffForksFromTheNewerBase(t *testing.T) {
	dir := stacked(t)
	git(t, dir, "switch", "-q", "base")
	commitFile(t, dir, "unpushed.txt")
	git(t, dir, "switch", "-q", "-c", "feat2")
	commitFile(t, dir, "feat2.txt")
	git(t, dir, "config", "branch.feat2."+cfgAfterBranch, "base")

	out, _, err := runPR(t, dir, "diff", "--name-only")
	if err != nil || out != "feat2.txt\n" {
		t.Errorf("diff = %q, %v; want feat2.txt alone", out, err)
	}
}

func TestPRViewWithNoPR(t *testing.T) {
	dir := stacked(t)

	out, _, err := runPR(t, dir, "view")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"branch: feat\n", "base: base\n", "no open PR for feat\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
}

func TestPullNumberOf(t *testing.T) {
	for token, want := range map[string]int{
		"12":                             12,
		"#12":                            12,
		"!12":                            12,
		"https://github.com/o/r/pull/12": 12,
		"https://gitlab.example.com/g/r/-/merge_requests/7": 7,
		"FT-12":                            0,
		"https://github.com/o/r/issues/12": 0,
		"":                                 0,
	} {
		if got := pullNumberOf(token); got != want {
			t.Errorf("pullNumberOf(%q) = %d, want %d", token, got, want)
		}
	}
}

// Only an open request is the branch's: a closed one before it is history.
func TestOpenPullOfSkipsClosed(t *testing.T) {
	f := newFakes(t)
	f.script("gh pr list --head feat --state all --json number,state,headRefName,headRefOid,baseRefName,url,"+
		"author,createdAt,mergedAt,closedAt --limit 100",
		`[{"number":3,"state":"CLOSED","createdAt":"2026-10-02T00:00:00Z"},`+
			`{"number":5,"state":"OPEN","createdAt":"2026-10-01T00:00:00Z"}]`)

	tg := &target{dir: t.TempDir(), pb: &probe.Probes{CLI: probe.Signal{Value: "gh"}}}

	if p, err := openPullOf(t.Context(), tg, "feat"); err != nil || p.Number != 5 {
		t.Errorf("openPullOf = %d, %v; want 5", p.Number, err)
	}
}

// A finished task's branch is gone, so its newest request, merged or not,
// stands for the change.
func TestLastPullOfTakesTheNewestAnyState(t *testing.T) {
	f := newFakes(t)
	f.script("gh pr list --head REPO-7 --state all --json number,state,headRefName,headRefOid,baseRefName,url,"+
		"author,createdAt,mergedAt,closedAt --limit 100",
		`[{"number":3,"state":"CLOSED","createdAt":"2026-10-01T00:00:00Z"},`+
			`{"number":5,"state":"MERGED","baseRefName":"main","createdAt":"2026-10-02T00:00:00Z"}]`)

	tg := &target{dir: t.TempDir(), pb: &probe.Probes{CLI: probe.Signal{Value: "gh"}}}

	if p, err := lastPullOf(t.Context(), tg, "REPO-7"); err != nil || p.Number != 5 || p.Base != "main" {
		t.Errorf("lastPullOf = #%d on %q, %v; want #5 on main", p.Number, p.Base, err)
	}
}

// A task id finds its branch locally, or on origin alone, and names a task
// with no branch as one to start.
func TestPRSubjectByTaskID(t *testing.T) {
	dir := prRepo(t)
	git(t, dir, "switch", "-q", "-c", "REPO-7")
	commitFile(t, dir, "seven.txt")
	git(t, dir, "push", "-q", "origin", "REPO-7")
	git(t, dir, "switch", "-q", "-c", "REPO-8", "main")
	commitFile(t, dir, "eight.txt")
	git(t, dir, "switch", "-q", "main")

	out, _, err := runPR(t, dir, "view", "REPO-8")
	if err != nil || !strings.Contains(out, "branch: REPO-8\n") || !strings.Contains(out, "task: REPO-8\n") {
		t.Errorf("local branch: view = %q, %v", out, err)
	}

	if out, _, err := runPR(t, dir, "diff", "REPO-8", "--name-only"); err != nil || out != "eight.txt\n" {
		t.Errorf("local branch: diff = %q, %v; want its commits alone", out, err)
	}

	git(t, dir, "branch", "-q", "-D", "REPO-7")

	if out, _, err := runPR(t, dir, "diff", "REPO-7", "--name-only"); err != nil || out != "seven.txt\n" {
		t.Errorf("origin-only branch: diff = %q, %v; want seven.txt", out, err)
	}

	if _, _, err := runPR(t, dir, "view", "REPO-9"); err == nil || !strings.Contains(err.Error(), "start it first") {
		t.Errorf("no branch: err = %v, want the task named as one to start", err)
	}
}

// A PR's head branch names its task by the link start writes, else by the
// id in its name.
func TestBranchTask(t *testing.T) {
	dir := prRepo(t)
	git(t, dir, "config", "branch.picked-up."+cfgTaskID, "REPO-3")

	tg, err := targetAt(t.Context(), dir, false, "pr")
	if err != nil {
		t.Fatal(err)
	}

	named := tg.eff.IDPrefix + "-4"

	for branch, want := range map[string]string{"picked-up": "REPO-3", named + "/slug": named, "other": ""} {
		got := ""
		if r := branchTask(t.Context(), tg, dir, branch); r != nil {
			got = r.ID
		}

		if got != want {
			t.Errorf("branchTask(%q) = %q, want %q", branch, got, want)
		}
	}
}

func TestPullCommentsGitHub(t *testing.T) {
	f := newFakes(t)
	f.script("gh pr view 4 --json comments", `{"comments":[`+
		`{"author":{"login":"ann"},"body":"looks good","createdAt":"2026-10-01T00:00:00Z"}]}`)

	got, err := newHost("gh", t.TempDir()).PullComments(t.Context(), 4)
	if err != nil || len(got) != 1 || got[0].Author != "ann" || got[0].Body != "looks good" {
		t.Errorf("PullComments = %+v, %v", got, err)
	}
}

func TestPatchFiles(t *testing.T) {
	patch := "diff --git a/x.go b/x.go\nindex 1..2\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/old.md b/new.md\nrename from old.md\n"

	if got := patchFiles(patch); got != "x.go\nnew.md" {
		t.Errorf("patchFiles = %q", got)
	}
}

// After a squash merge the merge base is still where the first PR started,
// so a commit added to the reused branch is all that is new.
func TestPRDiffShowsOnlyCommitsAfterASquashMerge(t *testing.T) {
	dir := prRepo(t)
	git(t, dir, "switch", "-q", "-c", "feat")
	commitFile(t, dir, "first.txt")
	git(t, dir, "switch", "-q", "main")
	git(t, dir, "merge", "-q", "--squash", "feat")
	git(t, dir, "commit", "-q", "-m", "first, squashed")
	git(t, dir, "push", "-q", "origin", "main")
	git(t, dir, "switch", "-q", "feat")
	commitFile(t, dir, "second.txt")

	out, _, err := runPR(t, dir, "diff", "--name-only")
	if err != nil || out != "second.txt\n" {
		t.Errorf("diff = %q, %v; want second.txt alone", out, err)
	}
}
