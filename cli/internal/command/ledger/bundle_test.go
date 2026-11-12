package ledger_test

import (
	"testing"

	ledgercmd "github.com/amberpixels/f10/cli/internal/command/ledger"
	"github.com/amberpixels/f10/cli/internal/ledger"
)

// A bundle's events carry its lead; a rider finds them through the branch.
func TestLedgerMatchesABundleMember(t *testing.T) {
	e := ledger.Event{Task: "GH-1", Branch: "GH-1+GH-2/slug"}

	for id, want := range map[string]bool{"GH-1": true, "gh-2": true, "GH-3": false} {
		if got := e.OnTask(id); got != want {
			t.Errorf("OnTask(%s) = %v, want %v", id, got, want)
		}
	}

	if got := ledgercmd.EventTask(e); got != "GH-1+GH-2" {
		t.Errorf("eventTask = %q, want the bundle", got)
	}

	if got := ledgercmd.EventTask(ledger.Event{Task: "GH-1", Branch: "GH-1/slug"}); got != "GH-1" {
		t.Errorf("eventTask on a single branch = %q, want GH-1", got)
	}
}
