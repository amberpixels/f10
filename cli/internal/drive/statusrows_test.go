package drive

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// A halt drive records reads back through the ledger's readers.
func TestHaltPayloadReadsBack(t *testing.T) {
	p := haltPayload(driveResult{Skill: "ship", Reason: "stalled"})

	e := ledger.MustEvent(ledger.TaskHalted, p)
	if e.Skill() != "ship" || e.Reason() != "stalled" {
		t.Errorf("halt %+v", p)
	}
}
