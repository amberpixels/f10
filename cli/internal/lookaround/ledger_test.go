package lookaround

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// `/f10:finish FT-12` run from a session whose run is FT-9 finishes FT-12:
// the row's task is the session's, the argument names the task finished.
func TestFinishedFromTakesFinishsArgument(t *testing.T) {
	finish := func(task string, args ...any) ledger.Event {
		p := ledger.NewPayload(ledger.Payload{"args": args})

		return ledger.Event{Kind: ledger.TaskFinished, Task: task, Payload: p}
	}

	events := []ledger.Event{
		finish("FT-9", "FT-12", "--yes"),
		finish("", "--yes", "8"),
		finish("FT-3", "--yes"),
		finish("", "--yes"),
	}

	l := &Local{Finished: map[string]bool{}}
	finishedFrom(events, func(token string) string {
		if !strings.Contains(token, "-") {
			return "FT-" + token
		}

		return token
	}, l)

	for _, id := range []string{"FT-12", "FT-8", "FT-3"} {
		if !l.Finished[id] {
			t.Errorf("%s not finished: %v", id, l.Finished)
		}
	}

	if l.Finished["FT-9"] || len(l.Finished) != 3 {
		t.Errorf("finished = %v, want FT-12, FT-8 and FT-3 only", l.Finished)
	}
}
