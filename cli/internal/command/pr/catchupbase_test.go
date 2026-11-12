package pr_test

import (
	"testing"
	"time"

	prcmd "github.com/amberpixels/f10/cli/internal/command/pr"
	"github.com/amberpixels/f10/cli/internal/state"
)

func TestShipStopped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")

	if prcmd.ShipStopped() {
		t.Error("no run file read as stopped")
	}

	for status, want := range map[string]bool{"failed": true, "partial": true, "running": false, "blocked": false} {
		if err := (&state.Run{Session: "sess-1", Ship: status}).Save(t.Context(), dir, time.Now()); err != nil {
			t.Fatal(err)
		}

		if got := prcmd.ShipStopped(); got != want {
			t.Errorf("ship %s: stopped %v, want %v", status, got, want)
		}
	}
}
