package finish

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// The watcher's finish record says who ran it; the drive's carries none.
func TestFinishedEvent(t *testing.T) {
	for _, by := range []string{"", "watch"} {
		e := FinishedEvent("GH-1", by, time.Time{})
		got, set := e.Payload.Val["by"]

		if e.Kind != ledger.TaskFinished || e.Source != ledger.Observed || e.Task != "GH-1" ||
			set != (by != "") || (set && got != by) {
			t.Errorf("by %q: %+v", by, e)
		}
	}
}
