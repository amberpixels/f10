package drive

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
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

// A red head goes to the agent as /f10:ci; a triage that leaves the same
// red head with nothing new tried halts, naming the job, while one that
// recorded a rerun is triaged again.
func TestDriveTriagesARedHead(t *testing.T) {
	f := testkit.NewFakes(t)
	in, _ := checksDrive()
	in.Main, in.StateDir, in.TTL = "/code/repo", t.TempDir(), state.DefaultTTL
	in.Finish = func(context.Context, io.Writer, DriveFinish) error {
		return finish.ChecksError{URL: "https://gh/pr/9", Checks: forge.HeadChecks{
			State: forge.Red, SHA: "h3ad", Failed: []forge.Check{{Name: "lint", URL: "https://ci/lint"}},
		}}
	}
	in.Tick = &tickView{
		Wss: []agentctl.Workspace{{ID: "ws:GH-1", Path: "/code/repo.GH-1", Status: agentctl.StatusIdle}},
	}

	r := &DriveRun{
		Task: DriveTask{Ref: testkit.Gh1}, O: start.OpenedTask{Path: "/code/repo.GH-1", Branch: "GH-1"}, Finishes: true,
	}
	r.O.Ws.ID = "ws:GH-1"

	prompt := "herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:ci GH-1 --driven"
	f.Script(prompt, "{}")

	// the answer to someone else's PR is taken once, so the triage keeps it
	in.Answers.Give(answer{Task: "GH-1", Text: "1. merge"})

	if err := in.finishOrHold(t.Context(), r); err != nil || r.Skill != "ci" || countCalls(f.Calls, prompt) != 1 {
		t.Fatalf("red = %v, skill %q, calls:\n%s\nwant /f10:ci sent", err, r.Skill, strings.Join(f.Calls, "\n"))
	}

	if r.checks == nil || r.checks.answer != finishMerge {
		t.Errorf("hold after the triage = %+v, want it to keep the merge answer", r.checks)
	}

	// the step reran the failed jobs: something moved, so it is sent again
	f.Meta.Seed("GH-1", branchmeta.CI, "h3ad:rerun")

	if err := in.finishOrHold(t.Context(), r); err != nil || countCalls(f.Calls, prompt) != 2 {
		t.Fatalf("red after a rerun = %v; want a second triage", err)
	}

	res, ok := endOf(in.finishOrHold(t.Context(), r))
	if !ok || res.Code != ui.ExitHalted || !strings.Contains(res.Reason, "lint  https://ci/lint") ||
		!strings.Contains(res.Reason, "nothing new tried") {
		t.Errorf("red with nothing new = %+v, want a halt naming lint", res)
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

	if got := taskStanding(drivestore.Task{Skill: "ci"}, true); got != "triaging checks" {
		t.Errorf("standing = %q, want triaging checks", got)
	}
}

// A drive that does not finish holds a shipped PR while its checks run, and
// ends the task once they pass or the budget runs out.
func TestDriveWithoutFinishHoldsOnChecks(t *testing.T) {
	in, now := checksDrive()
	states := []forge.HeadChecks{
		{State: forge.Pending, SHA: "h3ad", Running: []forge.Check{{Name: "test"}}},
		{State: forge.Green, SHA: "h3ad"},
	}
	in.Checks = func(context.Context, string) (forge.HeadChecks, string, error) {
		hc := states[0]
		if len(states) > 1 {
			states = states[1:]
		}

		return hc, "https://gh/pr/9", nil
	}

	r := &DriveRun{Task: DriveTask{Ref: testkit.Gh1}, Phase: atRunning}

	if err := in.endAfterChecks(t.Context(), r, []string{"ship"}); err != nil || r.Phase != atWaiting {
		t.Fatalf("pending = %v, phase %v; want held", err, r.Phase)
	}

	if res, ok := endOf(in.endAfterChecks(t.Context(), r, []string{"ship"})); !ok || res.Row != "done: ship" {
		t.Errorf("green = %+v, want done", res)
	}

	states = []forge.HeadChecks{{State: forge.Pending, SHA: "h3ad"}}
	r = &DriveRun{Task: DriveTask{Ref: testkit.Gh1}, Phase: atRunning}
	_ = in.endAfterChecks(t.Context(), r, []string{"ship"})
	*now = now.Add(finish.ChecksBudget)

	if res, ok := endOf(
		in.endAfterChecks(t.Context(), r, []string{"ship"}),
	); !ok ||
		!strings.Contains(res.Row, "checks still running") {
		t.Errorf("past the budget = %+v, want done with the checks still running", res)
	}

	// a drive that finishes leaves the checks to finish's gate
	r = &DriveRun{Task: DriveTask{Ref: testkit.Gh1}, Finishes: true}
	if res, ok := endOf(in.endAfterChecks(t.Context(), r, []string{"ship", "finish"})); !ok || res.Code != 0 {
		t.Errorf("finishing drive = %+v, want done", res)
	}
}

// The answer to someone else's PR is taken once: a finish held on checks
// runs again on that answer, and is never asked twice.
func TestDriveHoldKeepsTheFinishAnswer(t *testing.T) {
	in, _ := checksDrive()

	var got []string

	pending := true
	in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
		got = append(got, req.Pr)

		switch {
		case req.Pr == "":
			return finish.ForeignPullError{URL: "https://gh/pr/9", Author: "alice"}
		case pending:
			pending = false

			return finish.ChecksError{
				URL:    "https://gh/pr/9",
				Checks: forge.HeadChecks{State: forge.Pending, SHA: "h3ad"},
			}
		}

		return nil
	}

	run := &DriveRun{Task: DriveTask{Ref: testkit.Gh1}, Phase: atRunning}
	in.Answers.Give(answer{Task: "GH-1", Text: "1. merge"})

	if err := in.finishOrHold(t.Context(), run); err != nil || run.checks == nil {
		t.Fatalf("first finish = %v, hold %+v; want held on checks", err, run.checks)
	}

	res, ok := endOf(in.finishOrHold(t.Context(), run))
	if !ok || res.Code != 0 || strings.Join(got, ",") != "merge,merge" {
		t.Errorf("retry = %+v, finish passed %q; want finished on merge both times", res, got)
	}
}
