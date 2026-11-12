package scope_test

import (
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/calendar"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestLastStandupRoundTrip(t *testing.T) {
	testkit.IsolateEnv(t)

	if last, err := scope.ReadLastStandup(); err != nil || last != nil {
		t.Fatalf("no file: %v, %v, want nil, nil", last, err)
	}

	want := standup.Last{
		At:    time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC),
		Since: time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC),
	}
	if err := scope.WriteLastStandup(want); err != nil {
		t.Fatal(err)
	}

	got, err := scope.ReadLastStandup()
	if err != nil || got == nil || !got.At.Equal(want.At) || !got.Since.Equal(want.Since) {
		t.Errorf("read back %+v, %v, want %+v", got, err, want)
	}
}

// A stored standup that does not read is an error, never a silent restart
// of the period.
func TestLastStandupCorrupt(t *testing.T) {
	home := testkit.IsolateEnv(t)
	testkit.WriteUserFile(t, home, scope.StandupStateFile, "{not json")

	if _, err := scope.ReadLastStandup(); err == nil || !strings.Contains(err.Error(), scope.StandupStateFile) {
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
		home := testkit.IsolateEnv(t)
		testkit.WriteUserFile(t, home, scope.StandupConfigFile, c.body)

		got, err := scope.ReadWorkdays()
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
			}

			continue
		}

		var want calendar.Workdays
		for _, d := range c.want {
			want[d] = true
		}

		if err != nil || got != want {
			t.Errorf("%s: workdays = %v, %v, want %v", c.name, got, err, want)
		}
	}

	testkit.IsolateEnv(t)

	if got, err := scope.ReadWorkdays(); err != nil || got != calendar.DefaultWorkdays() {
		t.Errorf("no file: %v, %v, want Monday to Friday", got, err)
	}
}
