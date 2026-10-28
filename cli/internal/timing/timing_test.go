package timing

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

var t0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

// ev is one row of a scenario: minute, kind, task, and the payload.
func ev(minute float64, kind, task string, p ledger.Payload) ledger.Event {
	if p == nil {
		p = ledger.Payload{}
	}

	return ledger.Event{
		At: t0.Add(time.Duration(minute * float64(time.Minute))), Session: "s1", Task: task, Kind: kind,
		Payload: ledger.NewPayload(p),
	}
}

func run(minute float64, skill string) ledger.Event {
	return ev(minute, ledger.RunStarted, "", ledger.Payload{"skill": skill})
}

func claim(minute float64, args ...any) ledger.Event {
	return ev(minute, ledger.StateSet, "FT-1", ledger.Payload{"args": args})
}

func wait(from, to float64, reason string) []ledger.Event {
	return []ledger.Event{
		ev(from, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": reason}),
		ev(to, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": reason}),
	}
}

func join(parts ...any) []ledger.Event {
	var out []ledger.Event

	for _, p := range parts {
		switch p := p.(type) {
		case ledger.Event:
			out = append(out, p)
		case []ledger.Event:
			out = append(out, p...)
		}
	}

	return out
}

type got struct {
	step   string
	active float64 // minutes
}

func TestWindows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []ledger.Event
		want   []got
	}{
		{
			name: "a plan run counts to its saved plan, its waits removed",
			events: join(
				run(0, "plan"),
				wait(2, 5, "question"),
				ev(8, ledger.PlanSaved, "FT-1", nil),
			),
			want: []got{{"plan", 5}},
		},
		{
			name: "implement removes a question, a turn and a permission dialog",
			events: join(
				run(0, "ship"),
				claim(1, "ship", "running", "implement"),
				wait(2, 4, "question"),
				wait(5, 15, "turn"),
				wait(16, 17, "permission"),
				claim(20, "ship", "running", "cleanup"),
			),
			want: []got{{"implement", 6}},
		},
		{
			name: "overlapping waits subtract once",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "implement"),
				wait(2, 6, "turn"),
				wait(4, 8, "permission"),
				claim(10, "ship", "done"),
			),
			want: []got{{"implement", 4}},
		},
		{
			name: "a resent badge neither splits nor doubles a window",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "implement"),
				claim(3, "ship", "running", "implement"),
				claim(7, "ship", "running", "pr"),
			),
			want: []got{{"implement", 7}},
		},
		{
			name: "a blocked step resumed is two windows",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "implement"),
				claim(2, "ship", "blocked", "implement"),
				claim(30, "ship", "running", "implement"),
				claim(33, "ship", "running", "pr"),
			),
			want: []got{{"implement", 2}, {"implement", 3}},
		},
		{
			name: "ship's plan step ends at the next step; a reused plan counts nothing",
			events: join(
				run(0, "ship"),
				claim(0, "plan", "running"),
				claim(4, "ship", "running", "judge"),
				run(10, "ship"),
				claim(10, "plan", "running"),
				claim(11, "plan", "prior"),
				claim(12, "ship", "running", "implement"),
				claim(15, "ship", "done"),
			),
			want: []got{{"plan", 4}, {"implement", 3}},
		},
		{
			name: "the next run ends the step; a plan run that saves nothing counts nothing",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "implement"),
				run(5, "plan"),
			),
			want: []got{{"implement", 5}},
		},
		{
			name: "a wait that lost its end ends at the next wait",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "implement"),
				ev(3, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				claim(9, "ship", "running", "pr"),
			),
			want: []got{{"implement", 3}},
		},
		{
			name: "the Stop backstop ends the step",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "implement"),
				ev(6, ledger.SessionStopped, "FT-1", ledger.Payload{"backstop": true}),
			),
			want: []got{{"implement", 6}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// the turn the run ended on: every session since the release has one
			ws := Windows(join(tc.events, wait(100, 101, "turn")))

			var have []got
			for _, w := range ws {
				have = append(have, got{w.Step, w.Active.Minutes()})

				if w.Task != "FT-1" {
					t.Errorf("window %+v has task %q", w, w.Task)
				}
			}

			if len(have) != len(tc.want) {
				t.Fatalf("windows %v, want %v", have, tc.want)
			}

			for i := range have {
				if have[i] != tc.want[i] {
					t.Errorf("windows %v, want %v", have, tc.want)
				}
			}
		})
	}
}

// A session from before waits were recorded has no wait at all: its
// windows would be wall time, so it counts nothing.
func TestWindowsSkipASessionWithNoWaits(t *testing.T) {
	ws := Windows(join(run(0, "ship"), claim(0, "ship", "running", "implement"), claim(5, "ship", "done")))
	if len(ws) != 0 {
		t.Errorf("windows %+v", ws)
	}
}

// A wait nothing ended runs to the end of the record: the window it starts
// in has no active time to report.
func TestWindowsDropAWindowAnUnendedWaitRunsInto(t *testing.T) {
	ws := Windows(join(
		run(0, "ship"),
		claim(0, "ship", "running", "implement"),
		ev(3, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
		claim(9, "ship", "running", "pr"),
	))
	if len(ws) != 0 {
		t.Errorf("windows %+v", ws)
	}
}
