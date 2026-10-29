package timing

import (
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

var t0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

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
	e := ev(minute, ledger.StateSet, "FT-1", ledger.Payload{"args": args})
	e.Source = ledger.Claimed

	return e
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
		steps  []string // the steps compared; none is plan and implement
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
			name: "a turn whose end was lost ends at the next work",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "implement"),
				ev(2, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				ev(2, ledger.SessionStopped, "FT-1", nil),
				claim(4, "plan", "done"),
				ev(9, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": "turn"}),
				claim(10, "ship", "running", "pr"),
			),
			want: []got{{"implement", 8}},
		},
		{
			name: "the last turn's late observed row leaves the turn open",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "implement"),
				ev(2, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				ev(2.5, ledger.Pushed, "FT-1", nil),
				ev(9, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": "turn"}),
				claim(10, "ship", "running", "pr"),
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
		{
			name: "every flow entry is a window under its step's name",
			events: join(
				run(0, "ship"),
				claim(1, "ship", "running", "judge"),
				claim(2, "ship", "running", "implement"),
				claim(10, "ship", "running", "cleanup:comments"),
				claim(12, "ship", "running", "pr"),
				claim(15, "ship", "running", "judge"),
				claim(18, "ship", "running", "deploy:staging"),
				claim(20, "ship", "done"),
			),
			steps: []string{Judge, Implement, Cleanup, PR, "deploy"},
			want:  []got{{"judge", 1}, {"implement", 8}, {"cleanup", 2}, {"pr", 3}, {"judge", 3}, {"deploy", 2}},
		},
		{
			name: "a review entry splits into review and resolve at the findings file",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "review:local"),
				ev(6, ledger.ReviewRoundOpened, "FT-1", ledger.Payload{"round": "1"}),
				claim(7, "ship", "running", "review:local"),
				wait(8, 9, "question"),
				ev(10, ledger.ReviewRoundOpened, "FT-1", ledger.Payload{"round": "2"}),
				claim(14, "ship", "running", "judge"),
				claim(15, "ship", "done"),
			),
			steps: []string{Review, Resolve},
			want:  []got{{"review", 6}, {"resolve", 7}},
		},
		{
			name: "a resolve blocked on an ask and resumed stays resolve",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "review:local"),
				ev(5, ledger.ReviewRoundOpened, "FT-1", ledger.Payload{"round": "1"}),
				claim(8, "ship", "blocked", "review:local"),
				wait(9, 30, "turn"),
				claim(31, "ship", "running", "review:local"),
				claim(41, "ship", "running", "judge"),
				claim(42, "ship", "done"),
			),
			steps: []string{Review, Resolve},
			want:  []got{{"review", 5}, {"resolve", 3}, {"resolve", 10}},
		},
		{
			name: "a review entry with no findings file stays review",
			events: join(
				run(0, "ship"),
				claim(0, "ship", "running", "review:ci"),
				claim(4, "ship", "running", "pr"),
				claim(5, "ship", "done"),
			),
			steps: []string{Review, Resolve},
			want:  []got{{"review", 4}},
		},
		{
			name: "glue is the run outside its steps, to the turn after it is done",
			events: join(
				run(0, "ship"),
				claim(2, "ship", "running", "implement"),
				claim(10, "ship", "running", "pr"),
				wait(10.5, 11.5, "permission"),
				claim(14, "ship", "running", "pr"), // resent: no gap
				claim(15, "ship", "done"),
				ev(18, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				ev(30, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": "turn"}),
			),
			steps: []string{Glue},
			want:  []got{{"glue", 5}},
		},
		{
			name: "a plan run's glue is its hand-off, up to the chained ship",
			events: join(
				run(0, "plan"),
				ev(8, ledger.PlanSaved, "FT-1", nil),
				run(9, "ship"),
				claim(10, "ship", "running", "implement"),
				claim(20, "ship", "done"),
				ev(21, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				ev(22, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": "turn"}),
			),
			steps: []string{Glue},
			want:  []got{{"glue", 1}, {"glue", 2}},
		},
		{
			name: "a plan run that stops without a plan ends at its turn",
			events: join(
				run(0, "plan"),
				claim(2, "plan", "blocked", "judge"),
				ev(3, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				ev(10, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": "turn"}),
				run(100, "ship"),
				claim(100, "ship", "running", "implement"),
				claim(101, "ship", "done"),
				ev(102, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				ev(103, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": "turn"}),
			),
			steps: []string{Glue},
			want:  []got{{"glue", 3}, {"glue", 1}},
		},
		{
			name: "a driven run blocked and resumed is two glue segments",
			events: join(
				run(0, "ship"),
				claim(1, "ship", "running", "judge"),
				claim(3, "ship", "blocked", "judge"),
				ev(4, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				ev(40, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": "turn"}),
				claim(41, "ship", "running", "judge"),
				claim(45, "ship", "running", "implement"),
				claim(50, "ship", "done"),
				ev(52, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "turn"}),
				ev(53, ledger.WaitEnded, "FT-1", ledger.Payload{"reason": "turn"}),
			),
			steps: []string{Glue},
			want:  []got{{"glue", 2}, {"glue", 2}},
		},
		{
			name: "a run that never stops has no glue, though its turns end",
			events: join(
				run(0, "ship"),
				claim(1, "ship", "running", "implement"),
				wait(5, 6, "turn"),
			),
			steps: []string{Glue},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// the turn the run ended on: every session since the release has one
			ws := Windows(join(tc.events, wait(100, 101, "turn")))

			steps := tc.steps
			if steps == nil {
				steps = []string{Plan, Implement}
			}

			var have []got
			for _, w := range ws {
				if !slices.Contains(steps, w.Step) {
					continue
				}

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

// A question nothing ended runs to the end of the record: the window it
// starts in has no active time to report.
func TestWindowsDropAWindowAnUnendedWaitRunsInto(t *testing.T) {
	ws := Windows(join(
		run(0, "ship"),
		claim(0, "ship", "running", "implement"),
		ev(3, ledger.WaitStarted, "FT-1", ledger.Payload{"reason": "question"}),
		claim(9, "ship", "running", "pr"),
	))
	if len(ws) != 0 {
		t.Errorf("windows %+v", ws)
	}
}
