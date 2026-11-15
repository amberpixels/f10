package standup

import (
	"slices"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/ledger"
)

const mine = "/repo"

func row(h int, task, kind string, p any) ledger.Event {
	e := ledger.MustEvent(kind, p)
	e.At, e.Session, e.Task, e.Checkout, e.Branch = at(h), "s-"+task, task, mine, task+"/work"

	return e
}

func claim(h int, task, kind string, p ledger.ClaimPayload) ledger.Event {
	return row(h, task, kind, p)
}

func withLedger(in Input, events ...ledger.Event) Input {
	in.Ledger = &Ledger{Events: events, Checkouts: []string{mine}, Until: at(48)}

	return in
}

func kinds(as []Activity) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Kind+": "+a.Text)
	}

	return out
}

// A task that only got a plan and a debug session appears, with both.
func TestLedgerOnlyTaskAppears(t *testing.T) {
	in := withLedger(input(nil, nil),
		claim(2, "FT-7", ledger.StateSet, ledger.ClaimPayload{Plan: "running", Leaf: "debug"}),
		row(3, "FT-7", ledger.RegressionFound, ledger.RegressionFoundPayload{OriginTask: "FT-2", Repro: "full"}),
		row(4, "FT-7", ledger.PlanSaved, ledger.PlanSavedPayload{Task: "FT-7"}),
		row(4, "FT-7", ledger.PlanSaved, ledger.PlanSavedPayload{Task: "FT-7"}),
		claim(5, "FT-7", ledger.StateSet, ledger.ClaimPayload{Plan: "done"}),
	)

	task := taskByID(t, Group(in), "FT-7")

	want := []string{"debug: debug: cause found: regression of FT-2", "plan: plan saved"}
	if got := kinds(task.Activity); !slices.Equal(got, want) {
		t.Errorf("activity = %q, want %q", got, want)
	}

	if task.Stage != "planned, not started" || task.State != StateStarted || task.Class != ClassWork {
		t.Errorf("stage %q state %q class %q", task.Stage, task.State, task.Class)
	}
}

func TestOpenAskBlocksUntilAnswered(t *testing.T) {
	ask := claim(
		3,
		"FT-8",
		ledger.StateAsk,
		ledger.ClaimPayload{Ship: "blocked", Leaf: "judge", Ask: "1. Refactor? [yes | no]"},
	)
	carried := claim(
		4,
		"FT-8",
		ledger.StateNote,
		ledger.ClaimPayload{Ship: "blocked", Leaf: "judge", Ask: "1. Refactor? [yes | no]"},
	)

	task := taskByID(t, Group(withLedger(input(nil, nil), ask, carried)), "FT-8")
	if task.Blocked == nil || task.Blocked.Kind != BlockedAsk || !task.Blocked.Since.Equal(at(3)) {
		t.Fatalf("blocked = %+v, want the ask since its row", task.Blocked)
	}

	answered := claim(6, "FT-8", ledger.StateSet, ledger.ClaimPayload{Ship: "running", Leaf: "judge"})
	if task := taskByID(t, Group(withLedger(input(nil, nil), ask, carried, answered)), "FT-8"); task.Blocked != nil {
		t.Errorf("blocked after the answer: %+v", task.Blocked)
	}
}

func TestHaltShowsItsReason(t *testing.T) {
	in := withLedger(input(nil, nil),
		claim(2, "FT-9", ledger.StateSet, ledger.ClaimPayload{Ship: "running", Leaf: "pr"}),
		row(3, "FT-9", ledger.TaskHalted, ledger.TaskHaltedPayload{Skill: "ship", Reason: "no progress at pr"}),
	)

	task := taskByID(t, Group(in), "FT-9")
	if task.Blocked == nil || task.Blocked.Kind != BlockedHalt || task.Blocked.Text != "ship: no progress at pr" {
		t.Errorf("blocked = %+v, want the halt", task.Blocked)
	}
}

func TestSecondReviewRound(t *testing.T) {
	in := withLedger(input([]Commit{commit("a1", "FT-10/x", "work", 1)}, nil),
		row(2, "FT-10", ledger.ReviewRoundOpened, ledger.ReviewRoundOpenedPayload{Round: "1"}),
		row(2, "FT-10", ledger.FindingVerdict, ledger.FindingVerdictPayload{Round: "1", Finding: "1", Verdict: "fix"}),
		row(2, "FT-10", ledger.FindingVerdict, ledger.FindingVerdictPayload{Round: "1", Finding: "2", Verdict: "skip"}),
		row(3, "FT-10", ledger.ReviewRoundOpened, ledger.ReviewRoundOpenedPayload{Round: "2"}),
		claim(3, "FT-10", ledger.StateSet, ledger.ClaimPayload{Ship: "running", Leaf: "review"}),
	)

	task := taskByID(t, Group(in), "FT-10")
	if task.Stage != "review round 2" {
		t.Errorf("stage = %q, want review round 2", task.Stage)
	}

	if got := kinds(task.Activity); !slices.Contains(got, "review: round 1: 1 fixed, 1 skipped") {
		t.Errorf("activity = %q", got)
	}
}

// A row from another clone of the project is someone else's; one from the
// worktree f10 start made, removed since, is still the person's.
func TestWhoseWork(t *testing.T) {
	theirs := row(2, "FT-11", ledger.PlanSaved, ledger.PlanSavedPayload{Task: "FT-11"})
	theirs.Checkout = "/home/other/repo"

	gone := row(2, "FT-12", ledger.PlanSaved, ledger.PlanSavedPayload{Task: "FT-12"})
	gone.Checkout = "/repo.FT-12-work"

	in := withLedger(input(nil, nil), theirs, gone)
	in.Ledger.WorktreeOf = func(branch string) string { return "/repo." + branch[:5] + "-work" }

	r := Group(in)
	if len(r.Tasks) != 1 || r.Tasks[0].ID != "FT-12" {
		t.Errorf("tasks = %+v, want FT-12 alone", r.Tasks)
	}
}

func TestActiveMinutesAndDrive(t *testing.T) {
	started := row(2, "", ledger.RunStarted, ledger.RunStartedPayload{Skill: "ship"})
	started.Session = "s-FT-13"
	in := withLedger(
		input(nil, nil),
		started,
		claim(
			2,
			"FT-13",
			ledger.StateSet,
			ledger.ClaimPayload{Ship: "running", Leaf: "implement", Args: []string{"ship", "running", "implement"}},
		),
		claim(3, "FT-13", ledger.StateSet, ledger.ClaimPayload{Ship: "done", Args: []string{"ship", "done"}}),
		row(3, "FT-13", ledger.WaitStarted, ledger.WaitStartedPayload{Reason: "turn"}),
	)
	in.Ledger.Drives = []*drivestore.Record{{
		Number: 12, Created: at(1),
		Tasks: []drivestore.Task{
			{ID: "FT-13", Phase: drivestore.PhaseEnded, Outcome: &drivestore.Outcome{Code: 0}},
			{ID: "FT-14", Phase: drivestore.PhaseEnded, Outcome: &drivestore.Outcome{Code: 4}},
		},
	}}

	r := Group(in)
	task := taskByID(t, r, "FT-13")

	if task.ActiveMinutes != 60 || task.Driven != 12 {
		t.Errorf("active %d driven %d, want 60 and 12", task.ActiveMinutes, task.Driven)
	}

	if want := []Drive{{Number: 12, Tasks: 2, Shipped: 1, NeedsYou: 1}}; !slices.Equal(r.Drives, want) {
		t.Errorf("drives = %+v, want %+v", r.Drives, want)
	}

	if !Contributed(Merge(since, "", []Report{r})) {
		t.Error("a ledger-only report did not contribute")
	}
}

func TestNoLedgerContributesNothing(t *testing.T) {
	r := Group(input([]Commit{commit("a1", "FT-1/x", "work", 1)}, nil))
	if Contributed(Merge(since, "", []Report{r})) || r.Tasks[0].ActiveMinutes != 0 {
		t.Errorf("no ledger contributed: %+v", r.Tasks[0])
	}
}
