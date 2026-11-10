package lookaround

import (
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
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

// A task re-planned after its finish is pending work, not finished.
func TestDeriveLeavesARePlannedTaskUnfinished(t *testing.T) {
	l := &Local{Finished: map[string]bool{"FT-1": true, "FT-2": true}, Plans: map[string]time.Time{"FT-2": time.Now()}}
	if !finishedNow(l, "FT-1") || finishedNow(l, "FT-2") {
		t.Errorf("FT-1 %v, FT-2 %v; want finished and pending", finishedNow(l, "FT-1"), finishedNow(l, "FT-2"))
	}
}

// A ledger claim reads back without the wait and done it recorded.
func TestClaimRunLeavesWaitAndDoneOff(t *testing.T) {
	r := &state.Run{Ship: "blocked", Leaf: "implement", Wait: "FT-2", Done: "catchup"}
	e := ledger.Event{Task: "FT-1", Kind: ledger.StateSet, Payload: ledger.NewPayload(ledger.EncodeClaim(r, nil))}

	if got := ClaimRun(e); got.Ship != "blocked" || got.Leaf != "implement" || got.Wait != "" || got.Done != "" {
		t.Errorf("claim %+v", got)
	}
}

// A ledger finish counts only while no live plan says the task came back,
// the rule the board's local collector applies too.
func TestAddLedgerKeepsARePlannedTaskOpen(t *testing.T) {
	var replanned, done TaskFacts

	replanned.AddLedger(LedgerTask{Finished: true}, true)
	done.AddLedger(LedgerTask{Finished: true}, false)

	if replanned.Finished || !done.Finished || done.FinishedFrom != FinishedByLedger {
		t.Errorf("re-planned %+v, finished %+v", replanned, done)
	}
}
