package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// stacked puts feat on a pushed base branch, recorded as start records it.
func stacked(t *testing.T) string {
	t.Helper()

	dir := testkit.PrRepo(t)
	testkit.Git(t, dir, "switch", "-q", "-c", "base")
	testkit.CommitFile(t, dir, "base.txt")
	testkit.Git(t, dir, "push", "-q", "origin", "base")
	testkit.Git(t, dir, "switch", "-q", "-c", "feat")
	testkit.CommitFile(t, dir, "feat.txt")
	testkit.Git(t, dir, "config", branchmeta.AfterBranch.For("feat"), "base")

	return dir
}

func TestPRBase(t *testing.T) {
	dir := stacked(t)

	if out, _, err := runApp(t, "-C", dir, "pr", "base").streams(); err != nil || out != "base\n" {
		t.Errorf("stacked: base = %q, %v; want base", out, err)
	}

	testkit.Git(t, dir, "config", branchmeta.AfterBranch.For("feat"), "gone")

	out, errOut, err := runApp(t, "-C", dir, "pr", "base").streams()
	if err != nil || out != "main\nfound: "+stack.FoundGone+"\n" || errOut != "" {
		t.Errorf("gone: base = %q, %q, %v; want main and the typed found line, nothing on stderr", out, errOut, err)
	}

	testkit.Git(t, dir, "config", "--unset", branchmeta.AfterBranch.For("feat"))

	if out, _, err := runApp(t, "-C", dir, "pr", "base").streams(); err != nil || out != "main\n" {
		t.Errorf("unrecorded: base = %q, %v; want main", out, err)
	}
}

// A PR cannot target a base origin lacks, so --origin refuses it.
func TestPRBaseOriginRefusesALocalBase(t *testing.T) {
	dir := testkit.PrRepo(t)
	testkit.Git(t, dir, "switch", "-q", "-c", "base")
	testkit.CommitFile(t, dir, "base.txt")
	testkit.Git(t, dir, "switch", "-q", "-c", "feat")
	testkit.Git(t, dir, "config", branchmeta.AfterBranch.For("feat"), "base")

	_, _, err := runApp(t, "-C", dir, "pr", "base", "--origin").streams()
	if err == nil || !strings.Contains(err.Error(), "not on origin") {
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

	out, _, err := runApp(t, "-C", dir, "pr", "diff", "--name-only").streams()
	if err != nil || out != "feat.txt\nmain.txt\n" {
		t.Errorf("diff = %q, %v; want feat.txt and the uncommitted main.txt", out, err)
	}
}

// The base's unpushed commit the branch was cut from is the base's work,
// and origin's copy of the base moving ahead is not this change either.
func TestPRDiffForksFromTheNewerBase(t *testing.T) {
	dir := stacked(t)
	testkit.Git(t, dir, "switch", "-q", "base")
	testkit.CommitFile(t, dir, "unpushed.txt")
	testkit.Git(t, dir, "switch", "-q", "-c", "feat2")
	testkit.CommitFile(t, dir, "feat2.txt")
	testkit.Git(t, dir, "config", branchmeta.AfterBranch.For("feat2"), "base")

	out, _, err := runApp(t, "-C", dir, "pr", "diff", "--name-only").streams()
	if err != nil || out != "feat2.txt\n" {
		t.Errorf("diff = %q, %v; want feat2.txt alone", out, err)
	}
}

func TestPRViewWithNoPR(t *testing.T) {
	dir := stacked(t)

	out, _, err := runApp(t, "-C", dir, "pr", "view").streams()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"branch: feat\n", "base: base\n", "no open PR for feat\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
}

// A task id finds its branch locally, or on origin alone, and names a task
// with no branch as one to start.
func TestPRSubjectByTaskID(t *testing.T) {
	dir := testkit.PrRepo(t)
	testkit.Git(t, dir, "switch", "-q", "-c", "REPO-7")
	testkit.CommitFile(t, dir, "seven.txt")
	testkit.Git(t, dir, "push", "-q", "origin", "REPO-7")
	testkit.Git(t, dir, "switch", "-q", "-c", "REPO-8", "main")
	testkit.CommitFile(t, dir, "eight.txt")
	testkit.Git(t, dir, "switch", "-q", "main")

	out, _, err := runApp(t, "-C", dir, "pr", "view", "REPO-8").streams()
	if err != nil || !strings.Contains(out, "branch: REPO-8\n") || !strings.Contains(out, "task: REPO-8\n") {
		t.Errorf("local branch: view = %q, %v", out, err)
	}

	out, _, err = runApp(t, "-C", dir, "pr", "diff", "REPO-8", "--name-only").streams()
	if err != nil || out != "eight.txt\n" {
		t.Errorf("local branch: diff = %q, %v; want its commits alone", out, err)
	}

	testkit.Git(t, dir, "branch", "-q", "-D", "REPO-7")

	out, _, err = runApp(t, "-C", dir, "pr", "diff", "REPO-7", "--name-only").streams()
	if err != nil || out != "seven.txt\n" {
		t.Errorf("origin-only branch: diff = %q, %v; want seven.txt", out, err)
	}

	_, _, err = runApp(t, "-C", dir, "pr", "view", "REPO-9").streams()
	if err == nil || !strings.Contains(err.Error(), "start it first") {
		t.Errorf("no branch: err = %v, want the task named as one to start", err)
	}
}

// After a squash merge the merge base is still where the first PR started,
// so a commit added to the reused branch is all that is new.
func TestPRDiffShowsOnlyCommitsAfterASquashMerge(t *testing.T) {
	dir := testkit.PrRepo(t)
	testkit.Git(t, dir, "switch", "-q", "-c", "feat")
	testkit.CommitFile(t, dir, "first.txt")
	testkit.Git(t, dir, "switch", "-q", "main")
	testkit.Git(t, dir, "merge", "-q", "--squash", "feat")
	testkit.Git(t, dir, "commit", "-q", "-m", "first, squashed")
	testkit.Git(t, dir, "push", "-q", "origin", "main")
	testkit.Git(t, dir, "switch", "-q", "feat")
	testkit.CommitFile(t, dir, "second.txt")

	out, _, err := runApp(t, "-C", dir, "pr", "diff", "--name-only").streams()
	if err != nil || out != "second.txt\n" {
		t.Errorf("diff = %q, %v; want second.txt alone", out, err)
	}
}

// A branch whose PR merged but which is not finished yet still shows its
// change, which explain and judge read after the merge.
func TestPRDiffOfAMergedBranchShowsItsChange(t *testing.T) {
	dir := testkit.PrRepo(t)
	testkit.Git(t, dir, "switch", "-q", "-c", "feat")
	testkit.CommitFile(t, dir, "first.txt")
	testkit.Git(t, dir, "switch", "-q", "main")
	testkit.Git(t, dir, "merge", "-q", "--squash", "feat")
	testkit.Git(t, dir, "commit", "-q", "-m", "first, squashed")
	testkit.Git(t, dir, "push", "-q", "origin", "main")
	testkit.Git(t, dir, "switch", "-q", "feat")

	out, _, err := runApp(t, "-C", dir, "pr", "diff", "--name-only").streams()
	if err != nil || out != "first.txt\n" {
		t.Errorf("diff = %q, %v; want first.txt", out, err)
	}
}
