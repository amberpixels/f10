package finish

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

const checks9 = testkit.GhChecks + "9"

func headChecks(contexts string) string {
	return `{"data":{"repository":{"pullRequest":{"commits":{"nodes":[{"commit":{"oid":"h3ad",` +
		`"statusCheckRollup":{"contexts":{"nodes":[` + contexts + `]}}}}]}}}}}`
}

const (
	lintRed     = `{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://ci/lint"}`
	testRunning = `{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS"}`
	testGreen   = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"}`
	// the merge pinned to the head whose checks passed
	pinnedMerge = "gh pr merge GH-1 --match-head-commit h3ad --squash"
)

// A red head refuses before anything changes, naming each failed job and
// how to merge anyway.
func TestFinishRefusesARedHead(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.f.Script(checks9, headChecks(lintRed))

	_, err := fx.run(t)

	var ce ChecksError
	if !errors.As(err, &ce) || ce.Pending() {
		t.Fatalf("err = %v, want a red ChecksError", err)
	}

	for _, want := range []string{"https://gh/pr/9 at h3ad has failing checks", "lint  https://ci/lint", "--ignore-checks"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want %q in it", err, want)
		}
	}

	for _, never := range []string{"gh pr merge", "git worktree remove", "herdr workspace close"} {
		if fx.f.Called(never) {
			t.Errorf("a refused finish ran %q", never)
		}
	}
}

// A pending head that turns green within the budget merges in the same run,
// pinned to the head it read.
func TestFinishWaitsForAPendingHead(t *testing.T) {
	seam.Swap(t, &ChecksPoll, 0)

	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.ChecksWait = time.Minute
	fx.f.Script(checks9, headChecks(testRunning), headChecks(testGreen))
	fx.f.Script(pinnedMerge, "")

	var steps []string

	fx.in.Progress = func(a string) { steps = append(steps, a) }

	if out, err := fx.run(t); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if !fx.f.Called(pinnedMerge) {
		t.Errorf("merge not pinned to h3ad:\n%s", strings.Join(fx.f.Calls, "\n"))
	}

	if !strings.Contains(strings.Join(steps, ","), "waiting on checks: test") {
		t.Errorf("progress = %q, want the wait named", steps)
	}
}

// Past the budget a pending head refuses like a red one, saying the checks
// were still running; with no wait it refuses at once, for the drive.
func TestFinishRefusesAPendingHeadPastTheBudget(t *testing.T) {
	seam.Swap(t, &ChecksPoll, 0)

	for _, wait := range []time.Duration{0, time.Millisecond} {
		fx := newFinishFixture(t)
		fx.openPR()
		fx.in.ChecksWait = wait
		fx.f.Script(checks9, headChecks(testRunning))

		_, err := fx.run(t)

		var ce ChecksError
		if !errors.As(err, &ce) || !ce.Pending() || !strings.Contains(err.Error(), "checks still running (test)") {
			t.Errorf("wait %v: err = %v, want a pending refusal naming test", wait, err)
		}

		if fx.f.Called("gh pr merge") {
			t.Errorf("wait %v: merged a pending head", wait)
		}
	}
}

// --ignore-checks merges a red head without reading it; --yes alone does
// not, since automated finishes pass it.
func TestFinishIgnoreChecks(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.Flags.IgnoreChecks = true
	fx.f.Script(checks9, headChecks(lintRed))

	if out, err := fx.run(t); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if fx.f.Called(testkit.GhChecks) || !fx.f.Called(testkit.GhMerge) {
		t.Errorf("--ignore-checks read the checks or did not merge:\n%s", strings.Join(fx.f.Calls, "\n"))
	}

	fx = newFinishFixture(t)
	fx.openPR()
	fx.in.Flags.Yes = true
	fx.f.Script(checks9, headChecks(lintRed))

	if _, err := fx.run(t); !errors.As(err, new(ChecksError)) {
		t.Errorf("--yes: err = %v, want the red refusal", err)
	}
}

// The watcher finishes only merged requests, so it never reads the checks.
func TestFinishMergedOnlyReadsNoChecks(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.Script(testkit.GhView, testkit.PrMerged)
	fx.in.Flags.MergedOnly = true

	if out, err := fx.run(t); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if fx.f.Called(testkit.GhChecks) {
		t.Error("a merged-only finish read the checks")
	}
}
