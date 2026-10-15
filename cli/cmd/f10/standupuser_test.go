package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/standup"
)

// runStandupApp runs `f10 [args...]` through the real command tree.
func runStandupApp(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer = &out

	err := app.Run(context.Background(), append([]string{"f10"}, args...))

	return out.String(), err
}

func writeUserFile(t *testing.T, home, name, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(home, ".f10"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(home, ".f10", name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLastStandupRoundTrip(t *testing.T) {
	isolateEnv(t)

	if last, err := readLastStandup(); err != nil || last != nil {
		t.Fatalf("no file: %v, %v, want nil, nil", last, err)
	}

	want := standup.Last{
		At:    time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC),
		Since: time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC),
	}
	if err := writeLastStandup(want); err != nil {
		t.Fatal(err)
	}

	got, err := readLastStandup()
	if err != nil || got == nil || !got.At.Equal(want.At) || !got.Since.Equal(want.Since) {
		t.Errorf("read back %+v, %v, want %+v", got, err, want)
	}
}

// A stored standup that does not read is an error, never a silent restart
// of the period.
func TestLastStandupCorrupt(t *testing.T) {
	home := isolateEnv(t)
	writeUserFile(t, home, standupStateFile, "{not json")

	if _, err := readLastStandup(); err == nil || !strings.Contains(err.Error(), standupStateFile) {
		t.Errorf("err = %v, want one naming the file", err)
	}
}

func TestReadWorkdays(t *testing.T) {
	cases := []struct {
		name, body string
		want       []time.Weekday
		wantErr    string
	}{
		{"no frontmatter", "# notes\n", []time.Weekday{1, 2, 3, 4, 5}, ""},
		{"no workdays key", "---\n---\nprose\n", []time.Weekday{1, 2, 3, 4, 5}, ""},
		{
			"a working Saturday",
			"---\nworkdays: [Mon, tue, wed, thu, fri, sat]\n---\n",
			[]time.Weekday{1, 2, 3, 4, 5, 6},
			"",
		},
		{"an unknown key", "---\nholidays: [2026-12-25]\n---\n", nil, "holidays"},
		{"a bad day name", "---\nworkdays: [monday]\n---\n", nil, `"monday"`},
		{"no day at all", "---\nworkdays: []\n---\n", nil, "names no day"},
	}

	for _, c := range cases {
		home := isolateEnv(t)
		writeUserFile(t, home, standupConfigFile, c.body)

		got, err := readWorkdays()
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
			}

			continue
		}

		var want standup.Workdays
		for _, d := range c.want {
			want[d] = true
		}

		if err != nil || got != want {
			t.Errorf("%s: workdays = %v, %v, want %v", c.name, got, err, want)
		}
	}

	isolateEnv(t)

	if got, err := readWorkdays(); err != nil || got != standup.DefaultWorkdays() {
		t.Errorf("no file: %v, %v, want Monday to Friday", got, err)
	}
}

func TestStandupMark(t *testing.T) {
	isolateEnv(t)

	if _, err := runStandupApp(t, "standup", "mark", "--since", "2026-10-09T07:00:00Z"); err != nil {
		t.Fatal(err)
	}

	last, err := readLastStandup()
	if err != nil || last == nil || !last.Since.Equal(time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("stored %+v, %v, want the given since", last, err)
	}

	if time.Since(last.At) > time.Minute {
		t.Errorf("at = %v, want now", last.At)
	}

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := runStandupApp(t, "standup", "mark", "--since", future); err == nil {
		t.Error("mark took a start in the future")
	}
}

func TestStandupSinceAndWeekConflict(t *testing.T) {
	isolateEnv(t)

	if _, err := runStandupApp(t, "standup", "--since", "2026-10-09", "--week"); err == nil ||
		!strings.Contains(err.Error(), "--week") {
		t.Errorf("err = %v, want --since and --week refused together", err)
	}
}

// With nothing stored, standup starts at the last working day with
// activity, found in one wider read of the history; reading the report
// stores nothing.
func TestStandupFallsBackToTheLastWorkingDay(t *testing.T) {
	isolateEnv(t)

	dir := canonical(t, t.TempDir())
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "me@x.io")
	git(t, dir, "config", "user.name", "me")

	now := time.Now()
	day := standup.LastWorkingDay(now, standup.DefaultWorkdays(), nil)
	worked := day.Add(10 * time.Hour)

	commit := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "work")
	commit.Dir = dir
	commit.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+worked.Format(time.RFC3339), "GIT_COMMITTER_DATE="+worked.Format(time.RFC3339))

	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	out, err := runStandupApp(t, "-C", dir, "standup", "--json")
	if err != nil {
		t.Fatal(err)
	}

	var rep standup.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if rep.Period != standup.PeriodLastWorkingDay || !rep.Since.Equal(day) {
		t.Errorf("period = %s since %v, want %s since %v", rep.Period, rep.Since, standup.PeriodLastWorkingDay, day)
	}

	if len(rep.Days) != 1 || rep.Days[0] != day.Format(time.DateOnly) {
		t.Errorf("days = %v, want [%s]", rep.Days, day.Format(time.DateOnly))
	}

	if last, err := readLastStandup(); err != nil || last != nil {
		t.Errorf("stored %+v, %v: reading a report must store nothing", last, err)
	}
}
