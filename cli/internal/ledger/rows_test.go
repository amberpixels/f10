package ledger

import "testing"

// Each reader reads its field from the kinds that carry it, and nothing from
// any other kind.
func TestRowReaders(t *testing.T) {
	if got := MustEvent(RunStarted, RunStartedPayload{Skill: "ship"}).Skill(); got != "ship" {
		t.Errorf("skill %q", got)
	}

	if got := MustEvent(TaskHalted, TaskHaltedPayload{Skill: "ship", Reason: "stall"}).Reason(); got != "stall" {
		t.Errorf("halt reason %q", got)
	}

	if got := MustEvent(WaitStarted, WaitStartedPayload{Reason: "turn"}).Reason(); got != "turn" {
		t.Errorf("reason %q", got)
	}

	if !MustEvent(SessionStopped, SessionStoppedPayload{Backstop: true}).Backstop() {
		t.Error("backstop not read")
	}

	if got := MustEvent(PlanSaved, PlanSavedPayload{Path: "p"}).Skill(); got != "" {
		t.Errorf("a plan row read skill %q", got)
	}
}
