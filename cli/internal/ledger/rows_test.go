package ledger

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/observe"
)

// Each reader takes its key from the observation that writes it.
func TestRowReadersMatchTheObservations(t *testing.T) {
	if got := (Event{Payload: payloadOf(&observe.SkillStarted{Skill: "ship"})}).Skill(); got != "ship" {
		t.Errorf("skill %q", got)
	}

	if got := (Event{Payload: payloadOf(&observe.WaitStarted{Reason: "turn"})}).Reason(); got != "turn" {
		t.Errorf("reason %q", got)
	}

	if !(Event{Payload: payloadOf(&observe.SessionStopped{Backstop: true})}).Backstop() {
		t.Error("backstop not read")
	}
}
