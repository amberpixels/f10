package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// spike gives the fixture shared storage in main's .f10 with GH-1's plan
// and spike report, and the experiment uncommitted in the worktree.
func (fx *finishFixture) spike(t *testing.T) string {
	t.Helper()

	fx.in.plansDir = filepath.Join(fx.main, ".f10", "plans")
	writeFile(t, filepath.Join(fx.in.plansDir, "GH-1.md"), "Roles: senior engineer\nKind: spike\n\n# GH-1\n")
	writePlan(t, filepath.Join(fx.main, ".f10", "spikes", "GH-1.md"))
	fx.f.script("git status --porcelain", " M cli/a.go\n?? probe.txt")
	fx.f.script("git rev-list --count refs/heads/main..refs/heads/GH-1", "0")

	return filepath.Join(fx.in.plansDir, "archive")
}

// A spike with no PR closes without a merge: the plan and the report
// archived side by side, dependents released, the experiment discarded
// before the worktree and branch go, and the task left for the tracker.
func TestFinishClosesASpike(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	archive := fx.spike(t)
	fx.f.script("git reset --hard", "")
	fx.f.script("git clean -fd", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, name := range []string{"GH-1.md", "GH-1.spike.md"} {
		if !exists(filepath.Join(archive, name)) {
			t.Errorf("%s is not archived", name)
		}
	}

	for _, want := range []string{
		"spike:", "GH-1.spike.md", "the experiment is discarded: cli/a.go, probe.txt", "no PR closes GH-1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}

	for _, never := range []string{"git merge-tree", "git commit-tree", "git merge", "git update-ref refs/heads/main", "gh pr merge"} {
		if fx.f.called(never) {
			t.Errorf("%q ran on a spike close", never)
		}
	}

	reset := slices.Index(fx.f.calls, "git reset --hard")
	removed := slices.Index(fx.f.calls, "git worktree remove "+fx.wt)

	if reset < 0 || removed < reset || !fx.f.called(cfgDeps) || !fx.f.called("git branch -D GH-1") ||
		!fx.f.called("herdr workspace close ws:9") {
		t.Errorf("calls = %q", fx.f.calls)
	}
}

// --keep commits the experiment and keeps it on a branch that names no
// task, so every lookup reads the spike as finished.
func TestFinishKeepsASpikesExperiment(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.spike(t)
	fx.in.keep = true
	fx.f.script("git rev-parse -q --verify refs/heads/kept/spike", "")
	fx.f.script("git add -A", "")
	fx.f.script("git commit --no-verify -m GH-1: spike experiment, not for merge", "")
	fx.f.script("git branch kept/spike refs/heads/GH-1", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if !strings.Contains(out, "branch:") || !strings.Contains(out, "kept on kept/spike") {
		t.Errorf("the report does not name the kept branch:\n%s", out)
	}

	commit := slices.Index(fx.f.calls, "git commit --no-verify -m GH-1: spike experiment, not for merge")
	removed := slices.Index(fx.f.calls, "git worktree remove "+fx.wt)
	kept := slices.Index(fx.f.calls, "git branch kept/spike refs/heads/GH-1")
	deleted := slices.Index(fx.f.calls, "git branch -D GH-1")

	if commit < 0 || removed < commit || kept < removed || deleted < kept {
		t.Errorf("calls = %q", fx.f.calls)
	}

	if fx.f.called("git reset") || fx.f.called("git clean") {
		t.Errorf("a kept experiment was discarded: %q", fx.f.calls)
	}
}

// A spike with a PR finishes the normal way, which needs a clean tree, and
// archives the report with the plan.
func TestFinishASpikeWithAPR(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()
		archive := fx.spike(t)
		fx.f.script("git status --porcelain", "")

		if out, err := fx.run(t); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		if !fx.f.called(ghMerge) || !exists(filepath.Join(archive, "GH-1.spike.md")) {
			t.Errorf("calls = %q", fx.f.calls)
		}
	})

	t.Run("dirty", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()
		fx.spike(t)

		_, err := fx.run(t)
		if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.called(ghMerge) || fx.f.called("git reset") {
			t.Errorf("calls = %q", fx.f.calls)
		}
	})
}

// The main checkout's tree may hold anything, so a spike shipped there is
// refused dirty, before the host is asked.
func TestFinishRefusesADirtySpikeOnMain(t *testing.T) {
	fx := newFinishFixture(t)
	fx.onMain(t, "# plan")
	fx.spike(t)

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("err = %v", err)
	}

	if fx.f.called("gh ") || fx.f.called("git reset") {
		t.Errorf("calls = %q", fx.f.calls)
	}
}

func TestFinishKeepNeedsASpike(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.keep = true

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "has no spike report") {
		t.Fatalf("err = %v", err)
	}
}

func TestKeptName(t *testing.T) {
	cases := []struct {
		branch string
		ids    []string
		want   string
	}{
		{"FT-115/close-a-spike", []string{"FT-115"}, "kept/close-a-spike"},
		{"feat/FT-1-slug", []string{"FT-1"}, "kept/feat/slug"},
		{"GH-1+GH-2/x", []string{"GH-1", "GH-2"}, "kept/x"},
		{"GH-1", []string{"GH-1"}, "kept/spike"},
		{"feat/ft-115-slug", []string{"FT-115"}, "kept/feat/slug"},
		{"FT115/slug", []string{"FT-115"}, "kept/slug"},
		{"GH-12/x", []string{"GH-1"}, "kept/GH-12/x"},
	}

	for _, c := range cases {
		f := newFakes(t)
		f.script("git rev-parse -q --verify refs/heads/"+c.want, "")

		if got := keptName(t.Context(), "/repo", c.branch, c.ids); got != c.want {
			t.Errorf("keptName(%q) = %q, want %q", c.branch, got, c.want)
		}
	}

	f := newFakes(t)
	f.script("git rev-parse -q --verify refs/heads/kept/x", "abc")
	f.script("git rev-parse -q --verify refs/heads/kept/x-2", "")

	if got := keptName(t.Context(), "/repo", "GH-1/x", []string{"GH-1"}); got != "kept/x-2" {
		t.Errorf("a taken name = %q", got)
	}
}

// An archived spike report counts the task finished, as a plan does.
func TestFinishedTaskBySpikeReport(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, filepath.Join(dir, "GH-1.spike.md"))

	if !finishedTask(taskInfo{}, []string{dir}, "GH-1", false) {
		t.Error("an archived spike report did not count")
	}
}

// A report left under an id whose plan is not a spike's closes nothing:
// the dirty tree is refused as for any task.
func TestFinishIgnoresAReportUnderAPlanThatIsNoSpike(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.spike(t)
	writeFile(t, filepath.Join(fx.in.plansDir, "GH-1.md"), "Roles: senior engineer\n\n# GH-1\n")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("err = %v", err)
	}

	if fx.f.called("git reset") || fx.f.called("gh ") {
		t.Errorf("calls = %q", fx.f.calls)
	}
}

// A shared report moves only once the worktree is gone: a removal that
// fails leaves it in spikes/, so a rerun closes the spike again.
func TestFinishKeepsTheReportWhenTheRemovalFails(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.spike(t)
	fx.f.script("git reset --hard", "")
	fx.f.script("git clean -fd", "")
	fx.f.fail("git worktree remove "+fx.wt, shell.Result{Code: 1, Stderr: "locked"})

	if _, err := fx.run(t); err == nil {
		t.Fatal("a failed removal finished")
	}

	if !exists(filepath.Join(fx.main, ".f10", "spikes", "GH-1.md")) {
		t.Error("the report left spikes/ before the worktree went")
	}
}

// A worktree keeping its own .f10 holds the report: it moves to main's
// archive before the worktree, and with it, goes.
func TestFinishArchivesALegacyWorktreesSpikeReport(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.f.script("git status --porcelain", "?? probe.txt")
	fx.f.script("git rev-list --count refs/heads/main..refs/heads/GH-1", "0")
	fx.f.script("git reset --hard", "")
	fx.f.script("git clean -fd", "")
	fx.plan(t, "GH-1.md", "Kind: spike\n")
	writeFile(t, filepath.Join(fx.wt, ".f10", "spikes", "GH-1.md"), "answer")
	fx.f.fail("git worktree remove "+fx.wt, shell.Result{Code: 1, Stderr: "locked"})

	if _, err := fx.run(t); err == nil {
		t.Fatal("a failed removal finished")
	}

	got, err := os.ReadFile(filepath.Join(fx.main, ".f10", "plans", "archive", "GH-1.spike.md"))
	if err != nil || string(got) != "answer" {
		t.Errorf("archived report = %q, %v", got, err)
	}
}
