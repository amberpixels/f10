package testkit

import (
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

func StatEvent(at time.Time, session, task, kind string, p ledger.Payload) ledger.Event {
	return ledger.Event{At: at, Session: session, Task: task, Kind: kind, Payload: ledger.NewPayload(p)}
}

// StatEvents is two tasks shipped a day apart: FT-1 with a 10-minute wait
// inside a 30-minute implement, a review entry, a pr and a size; FT-2 with
// no size.
func StatEvents(day time.Time) []ledger.Event {
	at := func(n int) time.Time { return day.Add(time.Duration(n) * time.Minute) }
	set := func(at time.Time, session, task string, args ...any) ledger.Event {
		return StatEvent(at, session, task, ledger.StateSet, ledger.Payload{"args": args})
	}

	return []ledger.Event{
		StatEvent(at(0), "s1", "", ledger.RunStarted, ledger.Payload{"skill": "plan"}),
		StatEvent(at(4), "s1", "FT-1", ledger.PlanSaved, nil),
		StatEvent(at(5), "s1", "", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		set(at(5), "s1", "FT-1", "ship", "running", "implement"),
		StatEvent(at(10), "s1", "FT-1", ledger.WaitStarted, ledger.Payload{"reason": "question"}),
		StatEvent(at(20), "s1", "FT-1", ledger.WaitEnded, ledger.Payload{"reason": "question"}),
		set(at(35), "s1", "FT-1", "ship", "running", "pr"),
		set(at(37), "s1", "FT-1", "ship", "running", "review:local"),
		StatEvent(at(40), "s1", "FT-1", ledger.ReviewRoundOpened, ledger.Payload{"round": "1"}),
		set(at(44), "s1", "FT-1", "ship", "running", "judge"),
		set(at(45), "s1", "FT-1", "ship", "done"),
		StatEvent(at(46), "s1", "FT-1", ledger.WaitStarted, ledger.Payload{"reason": "turn"}),
		StatEvent(at(60), "", "FT-1", ledger.TaskSized, ledger.Payload{"lines": 400.0, "md_lines": 100.0}),

		StatEvent(at(1440), "s2", "", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		set(at(1440), "s2", "FT-2", "ship", "running", "implement"),
		set(at(1446), "s2", "FT-2", "ship", "done"),
		StatEvent(at(1446), "s2", "FT-2", ledger.WaitStarted, ledger.Payload{"reason": "turn"}),
	}
}
