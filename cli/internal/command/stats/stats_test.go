package stats_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	statscmd "github.com/amberpixels/f10/cli/internal/command/stats"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestStatsTable(t *testing.T) {
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	var out bytes.Buffer
	if err := statscmd.WriteStats(&out, statscmd.StatRows(testkit.StatEvents(day), "", day)); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"task  plan  implement  review  deliver  glue  size  density",
		"FT-2  -  6.0m  -  -  0.0m  -  -",
		"FT-1  4.0m  20m  8.0m  2.0m  2.0m  400 (25% md)  5.0m",
		"median  4.0m  13m  8.0m  2.0m  1.0m  400  5.0m",
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
	events := testkit.StatEvents(day)

	if rows := statscmd.StatRows(events, "", day.Add(12*time.Hour)); len(rows) != 1 || rows[0].Task != "FT-2" {
		t.Errorf("since: %+v", rows)
	}

	if rows := statscmd.StatRows(events, "ft-1", day.Add(48*time.Hour)); len(rows) != 1 || rows[0].Task != "FT-1" {
		t.Errorf("task: %+v", rows)
	}
}

// A task named lists its steps in flow order, glue last, with a total.
func TestStatsSteps(t *testing.T) {
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	rows := statscmd.StatRows(testkit.StatEvents(day), "FT-1", day)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}

	var out bytes.Buffer
	if err := statscmd.WriteSteps(&out, rows[0]); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"step active", "plan 4.0m", "judge 1.0m", "implement 20m", "pr 2.0m",
		"review 3.0m", "resolve 4.0m", "glue 2.0m", "total 36m",
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got\n%s", out.String())
	}

	for i, w := range want {
		if got := strings.Join(strings.Fields(lines[i]), " "); got != w {
			t.Errorf("line %d = %q, want %q", i, got, w)
		}
	}
}

// Two pr entries, the second labelled, are two rows: the claim's labels and
// place name the entry, and the step group still sums both.
func TestStatsStepsByLabel(t *testing.T) {
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return day.Add(time.Duration(n) * time.Minute) }
	labels := []any{"", "", "", "pr-polish"}
	set := func(n int, leaf string, place float64) ledger.Event {
		return testkit.StatEvent(at(n), "s1", "FT-1", ledger.StateSet,
			ledger.Payload{"args": []any{"ship", "running", leaf}, "labels": labels, "at": place})
	}

	events := []ledger.Event{
		testkit.StatEvent(at(0), "s1", "", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		set(0, "implement", 1),
		testkit.StatEvent(at(1), "s1", "FT-1", ledger.WaitStarted, ledger.Payload{"reason": "question"}),
		testkit.StatEvent(at(2), "s1", "FT-1", ledger.WaitEnded, ledger.Payload{"reason": "question"}),
		set(10, "pr", 2),
		set(13, "review:local", 3),
		set(15, "pr[pr-polish]", 4),
		testkit.StatEvent(at(20), "s1", "FT-1", ledger.StateSet, ledger.Payload{"args": []any{"ship", "done"}}),
	}

	rows := statscmd.StatRows(events, "FT-1", day)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}

	if d := rows[0].Steps["pr"]; d != 8*time.Minute {
		t.Errorf("pr step = %v, want both entries' 8m", d)
	}

	var out bytes.Buffer
	if err := statscmd.WriteSteps(&out, rows[0]); err != nil {
		t.Fatal(err)
	}

	got := strings.Fields(out.String())
	pr, polish := slices.Index(got, "pr"), slices.Index(got, "pr-polish")

	if pr < 0 || polish != pr+2 || got[pr+1] != "3.0m" || got[polish+1] != "5.0m" {
		t.Errorf("breakdown:\n%s", out.String())
	}
}

// --since takes a count of days or weeks and nothing else.
func TestStatsCutoff(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

	for arg, want := range map[string]time.Time{"30d": now.AddDate(0, 0, -30), "2w": now.AddDate(0, 0, -14)} {
		if got, err := statscmd.StatsCutoff(arg, now); err != nil || !got.Equal(want) {
			t.Errorf("statsCutoff(%q) = %v, %v, want %v", arg, got, err, want)
		}
	}

	for _, bad := range []string{"", "0d", "3651d", "2026-10-01", "yesterday", "12h", "3mo", "x"} {
		if _, err := statscmd.StatsCutoff(bad, now); err == nil {
			t.Errorf("statsCutoff(%q) took it", bad)
		}
	}
}
