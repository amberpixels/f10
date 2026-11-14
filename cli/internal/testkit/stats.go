package testkit

import (
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// StatEvent is a row of kind with payload, the kind's current payload struct.
func StatEvent(at time.Time, session, task, kind string, payload any) ledger.Event {
	e := ledger.MustEvent(kind, payload)
	e.At, e.Session, e.Task = at, session, task

	return e
}

// StatEvents is two tasks shipped a day apart: FT-1 with a 10-minute wait
// inside a 30-minute implement, a review entry, a pr and a size; FT-2 with
// no size.
func StatEvents(day time.Time) []ledger.Event {
	at := func(n int) time.Time { return day.Add(time.Duration(n) * time.Minute) }
	set := func(at time.Time, session, task string, args ...string) ledger.Event {
		return StatEvent(at, session, task, ledger.StateSet, ledger.ClaimPayload{Args: args})
	}
	run := func(at time.Time, session, skill string) ledger.Event {
		return StatEvent(at, session, "", ledger.RunStarted, ledger.RunStartedPayload{Skill: skill})
	}
	wait := func(at time.Time, session, task, reason string) ledger.Event {
		return StatEvent(at, session, task, ledger.WaitStarted, ledger.WaitStartedPayload{Reason: reason})
	}

	return []ledger.Event{
		run(at(0), "s1", "plan"),
		StatEvent(at(4), "s1", "FT-1", ledger.PlanSaved, ledger.PlanSavedPayload{}),
		run(at(5), "s1", "ship"),
		set(at(5), "s1", "FT-1", "ship", "running", "implement"),
		wait(at(10), "s1", "FT-1", "question"),
		StatEvent(at(20), "s1", "FT-1", ledger.WaitEnded, ledger.WaitEndedPayload{Reason: "question"}),
		set(at(35), "s1", "FT-1", "ship", "running", "pr"),
		set(at(37), "s1", "FT-1", "ship", "running", "review:local"),
		StatEvent(at(40), "s1", "FT-1", ledger.ReviewRoundOpened, ledger.ReviewRoundOpenedPayload{Round: "1"}),
		set(at(44), "s1", "FT-1", "ship", "running", "judge"),
		set(at(45), "s1", "FT-1", "ship", "done"),
		wait(at(46), "s1", "FT-1", "turn"),
		StatEvent(at(60), "", "FT-1", ledger.TaskSized, ledger.TaskSizedPayload{Lines: 400, MdLines: 100}),

		run(at(1440), "s2", "ship"),
		set(at(1440), "s2", "FT-2", "ship", "running", "implement"),
		set(at(1446), "s2", "FT-2", "ship", "done"),
		wait(at(1446), "s2", "FT-2", "turn"),
	}
}
