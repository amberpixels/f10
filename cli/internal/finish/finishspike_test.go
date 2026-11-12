package finish

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// spike gives the fixture shared storage in main's .f10 with GH-1's plan
// and spike report, and the experiment uncommitted in the worktree.
func (fx *finishFixture) spike(t *testing.T) string {
	t.Helper()

	fx.in.Storage = storage.InRepo(fx.main)
	testkit.WriteFile(
		t,
		filepath.Join(fx.in.Storage.Plans(), "GH-1.md"),
		"Roles: senior engineer\nKind: spike\n\n# GH-1\n",
	)
	testkit.WritePlan(t, filepath.Join(fx.main, ".f10", "spikes", "GH-1.md"))
	fx.f.Script("git status --porcelain", " M cli/a.go\n?? probe.txt")
	fx.f.Script("git rev-list --count refs/heads/main..refs/heads/GH-1", "0")

	return filepath.Join(fx.in.Storage.Plans(), "archive")
}

// A spike with no PR closes without a merge: the experiment is discarded
// before the worktree and branch go, and the task is left for the tracker.
func TestFinishClosesASpike(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	archived := fx.spike(t)
	fx.f.Script("git reset --hard", "")
	fx.f.Script("git clean -fd", "")
	released := fx.dependent()

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, name := range []string{"GH-1.md", "GH-1.spike.md"} {
		if !fsx.Exists(filepath.Join(archived, name)) {
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
		if fx.f.Called(never) {
			t.Errorf("%q ran on a spike close", never)
		}
	}

	reset := slices.Index(fx.f.Calls, "git reset --hard")
	removed := slices.Index(fx.f.Calls, "git worktree remove "+fx.wt)

	if reset < 0 || removed < reset || !released() || !fx.f.Called("git branch -D GH-1") ||
		!fx.f.Called("herdr workspace close ws:9") {
		t.Errorf("calls = %q", fx.f.Calls)
	}
}

// --keep commits the experiment and keeps it on a branch that names no
// task, so every lookup reads the spike as finished; the kept branch is
// made before the worktree goes, so a failed removal is finished again.
func TestFinishKeepsASpikesExperiment(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.spike(t)
	fx.in.Flags.Keep = true
	fx.f.Script("git rev-parse -q --verify refs/heads/kept/spike", "")
	fx.f.Script("git add -A", "")
	fx.f.Script("git commit --no-verify -m GH-1: spike experiment, not for merge", "")
	fx.f.Script("git branch kept/spike refs/heads/GH-1", "")

	out, err := fx.run(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if !strings.Contains(out, "branch:") || !strings.Contains(out, "kept on kept/spike") {
		t.Errorf("the report does not name the kept branch:\n%s", out)
	}

	commit := slices.Index(fx.f.Calls, "git commit --no-verify -m GH-1: spike experiment, not for merge")
	removed := slices.Index(fx.f.Calls, "git worktree remove "+fx.wt)
	kept := slices.Index(fx.f.Calls, "git branch kept/spike refs/heads/GH-1")
	deleted := slices.Index(fx.f.Calls, "git branch -D GH-1")

	// the kept branch exists before the worktree goes
	if commit < 0 || kept < commit || removed < kept || deleted < removed {
		t.Errorf("calls = %q", fx.f.Calls)
	}

	if fx.f.Called("git reset") || fx.f.Called("git clean") {
		t.Errorf("a kept experiment was discarded: %q", fx.f.Calls)
	}
}

// A spike with a PR finishes the normal way, which needs a clean tree, and
// archives the report with the plan.
func TestFinishASpikeWithAPR(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		fx := newFinishFixture(t)
		fx.openPR()
		archived := fx.spike(t)
		fx.f.Script("git status --porcelain", "")

		if out, err := fx.run(t); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}

		if !fx.f.Called(testkit.GhMerge) || !fsx.Exists(filepath.Join(archived, "GH-1.spike.md")) {
			t.Errorf("calls = %q", fx.f.Calls)
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

		if fx.f.Called(testkit.GhMerge) || fx.f.Called("git reset") {
			t.Errorf("calls = %q", fx.f.Calls)
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

	if fx.f.Called("gh ") || fx.f.Called("git reset") {
		t.Errorf("calls = %q", fx.f.Calls)
	}
}

func TestFinishKeepNeedsASpike(t *testing.T) {
	fx := newFinishFixture(t)
	fx.in.Flags.Keep = true

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
		f := testkit.NewFakes(t)
		f.Script("git rev-parse -q --verify refs/heads/"+c.want, "")

		if got := KeptName(t.Context(), "/repo", c.branch, c.ids); got != c.want {
			t.Errorf("keptName(%q) = %q, want %q", c.branch, got, c.want)
		}
	}

	f := testkit.NewFakes(t)
	f.Script("git rev-parse -q --verify refs/heads/kept/x", "abc")
	f.Script("git rev-parse -q --verify refs/heads/kept/x-2", "")

	if got := KeptName(t.Context(), "/repo", "GH-1/x", []string{"GH-1"}); got != "kept/x-2" {
		t.Errorf("a taken name = %q", got)
	}
}

// A report left under an id whose plan is not a spike's closes nothing:
// the dirty tree is refused as for any task.
func TestFinishIgnoresAReportUnderAPlanThatIsNoSpike(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.spike(t)
	testkit.WriteFile(t, filepath.Join(fx.in.Storage.Plans(), "GH-1.md"), "Roles: senior engineer\n\n# GH-1\n")

	_, err := fx.run(t)
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("err = %v", err)
	}

	if fx.f.Called("git reset") || fx.f.Called("gh ") {
		t.Errorf("calls = %q", fx.f.Calls)
	}
}

// A shared report moves only once the worktree is gone: a removal that
// fails leaves it in spikes/, so a rerun closes the spike again.
func TestFinishKeepsTheReportWhenTheRemovalFails(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.spike(t)
	fx.f.Script("git reset --hard", "")
	fx.f.Script("git clean -fd", "")
	fx.f.Fail("git worktree remove "+fx.wt, shell.Result{Code: 1, Stderr: "locked"})

	if _, err := fx.run(t); err == nil {
		t.Fatal("a failed removal finished")
	}

	if !fsx.Exists(filepath.Join(fx.main, ".f10", "spikes", "GH-1.md")) {
		t.Error("the report left spikes/ before the worktree went")
	}
}

// A worktree keeping its own .f10 holds the report: it moves to main's
// archive before the worktree, and with it, goes.
func TestFinishArchivesALegacyWorktreesSpikeReport(t *testing.T) {
	fx := newFinishFixture(t)
	fx.noPull()
	fx.f.Script("git status --porcelain", "?? probe.txt")
	fx.f.Script("git rev-list --count refs/heads/main..refs/heads/GH-1", "0")
	fx.f.Script("git reset --hard", "")
	fx.f.Script("git clean -fd", "")
	fx.plan(t, "Kind: spike\n")
	testkit.WriteFile(t, filepath.Join(fx.wt, ".f10", "spikes", "GH-1.md"), "answer")
	fx.f.Fail("git worktree remove "+fx.wt, shell.Result{Code: 1, Stderr: "locked"})

	if _, err := fx.run(t); err == nil {
		t.Fatal("a failed removal finished")
	}

	got, err := os.ReadFile(filepath.Join(fx.main, ".f10", "plans", "archive", "GH-1.spike.md"))
	if err != nil || string(got) != "answer" {
		t.Errorf("archived report = %q, %v", got, err)
	}

	// the worktree's own copy goes with the worktree, so a rerun still finds it
	if TaskSpike(fx.wt, fx.in.Storage, "GH-1") == "" {
		t.Error("a rerun after the failed removal sees no spike")
	}
}

// The kind line is read in the shapes plans write it, and a kind that only
// starts with spike is not one.
func TestSpikePlan(t *testing.T) {
	for body, want := range map[string]bool{
		"Roles: x\nKind: spike\n":                      true,
		"- **Kind**: spike, from the label.\n":         true,
		"**Kind:** spike (the task asks a question)\n": true,
		"Kind: `spike`\n":                              true,
		"Kind: feature\n":                              false,
		"Kind: spiked\n":                               false,
		"# plan\n\nThe kind is not spike here.\n":      false,
	} {
		p := filepath.Join(t.TempDir(), "plan.md")
		testkit.WriteFile(t, p, body)

		if got := SpikePlan(p); got != want {
			t.Errorf("spikePlan(%q) = %v, want %v", body, got, want)
		}
	}
}
