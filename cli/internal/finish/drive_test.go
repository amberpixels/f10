package finish

import (
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// The watcher's finish record says who ran it; the drive's carries none.
func TestFinishedEvent(t *testing.T) {
	for _, by := range []string{"", "watch"} {
		e := FinishedEvent("GH-1", by, time.Time{})
		p, err := ledger.Decode[ledger.TaskFinishedPayload](e)

		if err != nil || e.Kind != ledger.TaskFinished || e.Source != ledger.Observed || e.Task != "GH-1" ||
			p.By != by || !slices.Equal(p.Args, []string{"GH-1"}) {
			t.Errorf("by %q: %+v", by, e)
		}
	}
}
