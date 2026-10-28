package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

func statEvent(at time.Time, session, task, kind string, p ledger.Payload) ledger.Event {
	return ledger.Event{At: at, Session: session, Task: task, Kind: kind, Payload: ledger.NewPayload(p)}
}

// statEvents is two tasks shipped a day apart: FT-1 with a 10-minute wait
// inside a 30-minute implement and a size, FT-2 with no size.
func statEvents(day time.Time) []ledger.Event {
	at := func(n int) time.Time { return day.Add(time.Duration(n) * time.Minute) }
	set := func(at time.Time, session, task string, args ...any) ledger.Event {
		return statEvent(at, session, task, ledger.StateSet, ledger.Payload{"args": args})
	}

	return []ledger.Event{
		statEvent(at(0), "s1", "", ledger.RunStarted, ledger.Payload{"skill": "plan"}),
		statEvent(at(4), "s1", "FT-1", ledger.PlanSaved, nil),
		statEvent(at(5), "s1", "", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		set(at(5), "s1", "FT-1", "ship", "running", "implement"),
		statEvent(at(10), "s1", "FT-1", ledger.WaitStarted, ledger.Payload{"reason": "question"}),
		statEvent(at(20), "s1", "FT-1", ledger.WaitEnded, ledger.Payload{"reason": "question"}),
		set(at(35), "s1", "FT-1", "ship", "done"),
		statEvent(at(60), "", "FT-1", ledger.TaskSized, ledger.Payload{"lines": 400.0, "md_lines": 100.0}),

		statEvent(at(1440), "s2", "", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		set(at(1440), "s2", "FT-2", "ship", "running", "implement"),
		set(at(1446), "s2", "FT-2", "ship", "done"),
		statEvent(at(1446), "s2", "FT-2", ledger.WaitStarted, ledger.Payload{"reason": "turn"}),
	}
}

func TestStatsTable(t *testing.T) {
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	var out bytes.Buffer
	if err := writeStats(&out, statRows(statEvents(day), "", day)); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"task  plan  implement          size  density",
		"FT-2     -       6.0m             -        -",
		"FT-1  4.0m        20m  400 (25% md)     5.0m",
		"median  4.0m        13m           400     5.0m",
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got\n%s", out.String())
	}

	for i, w := range want {
		if strings.Fields(lines[i])[0] != strings.Fields(w)[0] ||
			strings.Join(strings.Fields(lines[i]), " ") != strings.Join(strings.Fields(w), " ") {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

// --since keeps tasks active after the cutoff; a task named is shown
// whatever its age.
func TestStatsRowsSinceAndTask(t *testing.T) {
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	events := statEvents(day)

	if rows := statRows(events, "", day.Add(12*time.Hour)); len(rows) != 1 || rows[0].task != "FT-2" {
		t.Errorf("since: %+v", rows)
	}

	if rows := statRows(events, "ft-1", day.Add(48*time.Hour)); len(rows) != 1 || rows[0].task != "FT-1" {
		t.Errorf("task: %+v", rows)
	}
}
