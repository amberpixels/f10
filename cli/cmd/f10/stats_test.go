package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

func statEvent(at time.Time, session, task, kind string, p ledger.Payload) ledger.Event {
	return ledger.Event{At: at, Session: session, Task: task, Kind: kind, Payload: ledger.NewPayload(p)}
}

// statEvents is two tasks shipped a day apart: FT-1 with a 10-minute wait
// inside a 30-minute implement, a review entry, a pr and a size; FT-2 with
// no size.
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
		set(at(35), "s1", "FT-1", "ship", "running", "pr"),
		set(at(37), "s1", "FT-1", "ship", "running", "review:local"),
		statEvent(at(40), "s1", "FT-1", ledger.ReviewRoundOpened, ledger.Payload{"round": "1"}),
		set(at(44), "s1", "FT-1", "ship", "running", "judge"),
		set(at(45), "s1", "FT-1", "ship", "done"),
		statEvent(at(46), "s1", "FT-1", ledger.WaitStarted, ledger.Payload{"reason": "turn"}),
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
	events := statEvents(day)

	if rows := statRows(events, "", day.Add(12*time.Hour)); len(rows) != 1 || rows[0].task != "FT-2" {
		t.Errorf("since: %+v", rows)
	}

	if rows := statRows(events, "ft-1", day.Add(48*time.Hour)); len(rows) != 1 || rows[0].task != "FT-1" {
		t.Errorf("task: %+v", rows)
	}
}

// A task named lists its steps in flow order, glue last, with a total.
func TestStatsSteps(t *testing.T) {
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	rows := statRows(statEvents(day), "FT-1", day)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}

	var out bytes.Buffer
	if err := writeSteps(&out, rows[0]); err != nil {
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
		return statEvent(at(n), "s1", "FT-1", ledger.StateSet,
			ledger.Payload{"args": []any{"ship", "running", leaf}, "labels": labels, "at": place})
	}

	events := []ledger.Event{
		statEvent(at(0), "s1", "", ledger.RunStarted, ledger.Payload{"skill": "ship"}),
		set(0, "implement", 1),
		statEvent(at(1), "s1", "FT-1", ledger.WaitStarted, ledger.Payload{"reason": "question"}),
		statEvent(at(2), "s1", "FT-1", ledger.WaitEnded, ledger.Payload{"reason": "question"}),
		set(10, "pr", 2),
		set(13, "review:local", 3),
		set(15, "pr[pr-polish]", 4),
		statEvent(at(20), "s1", "FT-1", ledger.StateSet, ledger.Payload{"args": []any{"ship", "done"}}),
	}

	rows := statRows(events, "FT-1", day)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}

	if d := rows[0].steps["pr"]; d != 8*time.Minute {
		t.Errorf("pr step = %v, want both entries' 8m", d)
	}

	var out bytes.Buffer
	if err := writeSteps(&out, rows[0]); err != nil {
		t.Fatal(err)
	}

	got := strings.Fields(out.String())
	pr, polish := slices.Index(got, "pr"), slices.Index(got, "pr-polish")

	if pr < 0 || polish != pr+2 || got[pr+1] != "3.0m" || got[polish+1] != "5.0m" {
		t.Errorf("breakdown:\n%s", out.String())
	}
}

// -C answers for the checkout it names, and a bare number resolves to the
// project's id as every other command reads it.
func TestStatsTakesTheCheckoutAndABareNumber(t *testing.T) {
	repo := tracedRepo(t, "main2", "")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	t.Chdir(t.TempDir())

	ctx := t.Context()
	project := ledger.Where(ctx, repo).Project

	var events []ledger.Event

	for i, e := range statEvents(time.Now().Add(-2 * time.Hour)) {
		e.ID, e.Project = fmt.Sprintf("e%d", i), project
		e.Task = strings.Replace(e.Task, "FT-", "DM-", 1)
		events = append(events, e)
	}

	l, err := ledger.Open(ctx, ledger.Path(), time.Second, false)
	if err != nil {
		t.Fatal(err)
	}

	l.Append(ctx, events...)
	_ = l.Close()

	out, err := runApp(t, "-C", repo, "stats", "1").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "DM-1") {
		t.Errorf("stats -C %s 1 found no DM-1:\n%s", repo, out)
	}
}

// --since takes a count of days or weeks and nothing else.
func TestStatsCutoff(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

	for arg, want := range map[string]time.Time{"30d": now.AddDate(0, 0, -30), "2w": now.AddDate(0, 0, -14)} {
		if got, err := statsCutoff(arg, now); err != nil || !got.Equal(want) {
			t.Errorf("statsCutoff(%q) = %v, %v, want %v", arg, got, err, want)
		}
	}

	for _, bad := range []string{"", "0d", "3651d", "2026-10-01", "yesterday", "12h", "3mo", "x"} {
		if _, err := statsCutoff(bad, now); err == nil {
			t.Errorf("statsCutoff(%q) took it", bad)
		}
	}
}
