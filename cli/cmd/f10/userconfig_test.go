package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/calendar"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestStandupMark(t *testing.T) {
	testkit.IsolateEnv(t)

	if _, err := runApp(t, "standup", "mark", "--since", "2026-10-09T07:00:00Z").outErr(); err != nil {
		t.Fatal(err)
	}

	last, err := scope.ReadLastStandup()
	if err != nil || last == nil || !last.Since.Equal(time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("stored %+v, %v, want the given since", last, err)
	}

	if time.Since(last.At) > time.Minute {
		t.Errorf("at = %v, want now", last.At)
	}

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := runApp(t, "standup", "mark", "--since", future).outErr(); err == nil {
		t.Error("mark took a start in the future")
	}

	// the next standup starts when this one was read, not when it was marked
	_, err = runApp(t, "standup", "mark", "--since", "2026-10-09T07:00:00Z", "--at", "2026-10-12T06:30:00Z").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if last, err := scope.ReadLastStandup(); err != nil ||
		!last.At.Equal(time.Date(2026, 10, 12, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("stored %+v, %v, want at = --at", last, err)
	}

	_, err = runApp(t, "standup", "mark", "--since", "2026-10-12T07:00:00Z", "--at", "2026-10-12T06:30:00Z").outErr()
	if err == nil {
		t.Error("mark took a start after the read")
	}
}

// Only the fallback reads standup.md: a broken one fails no other run.
func TestStandupReadsWorkdaysOnlyOnTheFallback(t *testing.T) {
	home := testkit.IsolateEnv(t)
	testkit.WriteUserFile(t, home, scope.StandupConfigFile, "---\nworkdays: [monday]\n---\n")

	dir := testkit.InitRepo(t, testkit.Canonical(t, t.TempDir()), "master").Dir

	if _, err := runApp(t, "-C", dir, "standup", "--json", "--week").outErr(); err != nil {
		t.Errorf("--week: %v, want standup.md unread", err)
	}

	if _, err := runApp(t, "-C", dir, "standup", "--json").outErr(); err == nil ||
		!strings.Contains(err.Error(), "monday") {
		t.Errorf("fallback: err = %v, want the bad day named", err)
	}
}

func TestStandupSinceAndWeekConflict(t *testing.T) {
	testkit.IsolateEnv(t)

	if _, err := runApp(t, "standup", "--since", "2026-10-09", "--week").outErr(); err == nil ||
		!strings.Contains(err.Error(), "--week") {
		t.Errorf("err = %v, want --since and --week refused together", err)
	}
}

// With nothing stored, standup starts at the last working day with
// activity, found in one wider read of the history; reading the report
// stores nothing.
func TestStandupFallsBackToTheLastWorkingDay(t *testing.T) {
	testkit.IsolateEnv(t)

	r := testkit.InitRepo(t, testkit.Canonical(t, t.TempDir()), "master")
	dir := r.Dir

	now := time.Now()
	day := calendar.LastActiveWorkday(now, calendar.DefaultWorkdays(), nil)
	worked := day.Add(10 * time.Hour)

	r.At(worked.Format(time.RFC3339), "commit", "-q", "--allow-empty", "-m", "work")

	out, err := runApp(t, "-C", dir, "standup", "--json").outErr()
	if err != nil {
		t.Fatal(err)
	}

	var rep standup.Standup
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if rep.Period != standup.PeriodLastWorkingDay || !rep.Since.Equal(day) {
		t.Errorf("period = %s since %v, want %s since %v", rep.Period, rep.Since, standup.PeriodLastWorkingDay, day)
	}

	if len(rep.Days) != 1 || rep.Days[0] != day.Format(time.DateOnly) {
		t.Errorf("days = %v, want [%s]", rep.Days, day.Format(time.DateOnly))
	}

	if last, err := scope.ReadLastStandup(); err != nil || last != nil {
		t.Errorf("stored %+v, %v: reading a report must store nothing", last, err)
	}
}
