package drive

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// checksDrive is a drive whose finish answers from heads in turn: a sha
// with checks still running, else a merge. now moves the drive's clock.
func checksDrive(heads ...string) (*DriveInput, *time.Time) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	in := &DriveInput{
		Now: func() time.Time { return now },
		Finish: func(context.Context, io.Writer, DriveFinish) error {
			sha := heads[0]
			if len(heads) > 1 {
				heads = heads[1:]
			}

			if sha == "" {
				return nil
			}

			return finish.ChecksError{
				URL:    "https://gh/pr/9",
				Checks: forge.HeadChecks{State: forge.Pending, SHA: sha, Running: []forge.Check{{Name: "test"}}},
			}
		},
		Answers: NewAnswerBook(),
	}
	in.Out = io.Discard

	return in, &now
}

// A pending head holds the task waiting, as a base would, and the next
// poll's green head finishes it.
func TestDriveHoldsAPendingHead(t *testing.T) {
	in, _ := checksDrive("h3ad", "")
	run := &DriveRun{Task: DriveTask{Ref: testkit.Gh1}, Phase: atRunning}

	if err := in.finishOrHold(t.Context(), run); err != nil || run.Phase != atWaiting || run.checks == nil {
		t.Fatalf("finishOrHold = %v, phase %v, hold %+v; want held waiting", err, run.Phase, run.checks)
	}

	res, ok := endOf(in.finishOrHold(t.Context(), run))
	if !ok || res.Code != 0 || run.checks != nil {
		t.Errorf("after green = %+v, hold %+v; want finished, the hold dropped", res, run.checks)
	}
}

// Past the budget the hold halts with the checks still running; a push in
// the meantime starts a new budget.
func TestDriveHaltsAPendingHeadPastTheBudget(t *testing.T) {
	in, now := checksDrive("h3ad")
	run := &DriveRun{Task: DriveTask{Ref: testkit.Gh1}, Phase: atRunning}

	if err := in.finishOrHold(t.Context(), run); err != nil {
		t.Fatal(err)
	}

	*now = now.Add(finish.ChecksBudget)

	res, ok := endOf(in.finishOrHold(t.Context(), run))
	if !ok || res.Code != ui.ExitHalted || !strings.Contains(res.Reason, "checks still running (test) after 20m0s") {
		t.Errorf("past the budget = %+v, want a halt saying the checks were still running", res)
	}

	in, now = checksDrive("h3ad", "n3w")
	run = &DriveRun{Task: DriveTask{Ref: testkit.Gh1}, Phase: atRunning}
	_ = in.finishOrHold(t.Context(), run)
	*now = now.Add(finish.ChecksBudget)

	if err := in.finishOrHold(t.Context(), run); err != nil || run.checks.sha != "n3w" {
		t.Errorf("a new head = %v, hold %+v; want a fresh hold on n3w", err, run.checks)
	}
}

// A red head halts at once: only pending holds.
func TestDriveHaltsARedHead(t *testing.T) {
	in, _ := checksDrive()
	in.Finish = func(context.Context, io.Writer, DriveFinish) error {
		return finish.ChecksError{URL: "https://gh/pr/9", Checks: forge.HeadChecks{
			State: forge.Red, SHA: "h3ad", Failed: []forge.Check{{Name: "lint", URL: "https://ci/lint"}},
		}}
	}

	res, ok := endOf(in.finishOrHold(t.Context(), &DriveRun{Task: DriveTask{Ref: testkit.Gh1}}))
	if !ok || res.Code != ui.ExitHalted || !strings.Contains(res.Reason, "lint  https://ci/lint") {
		t.Errorf("red = %+v, want a halt naming lint", res)
	}
}

func TestTaskStandingWaitingOnChecks(t *testing.T) {
	waiting := drivestore.Task{Phase: drivestore.PhaseWaiting, Checks: "h3ad"}
	if got := taskStanding(waiting, true); got != "waiting on checks" {
		t.Errorf("standing = %q, want waiting on checks", got)
	}

	waiting.Checks = ""
	if got := taskStanding(waiting, true); got != "waiting to finish after its base" {
		t.Errorf("standing = %q, want the base wait", got)
	}
}
