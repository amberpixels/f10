package start

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/tasksource"
)

// A typo changes one digit and keeps the length: FT-570 for FT-530.
func TestOneDigitOff(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"FT-530", "FT-570", true},
		{"FT-530", "ft-531", true},
		{"FT-530", "FT-530", false},
		{"FT-530", "FT-571", false},
		{"FT-530", "FT-5300", false},
		{"FT-530", "GH-570", false},
		{"FT-530", "FT-57x", false},
	} {
		if got := oneDigitOff(c.a, c.b); got != c.want {
			t.Errorf("oneDigitOff(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// A commit ships the task when its subject opens with one of the ids, or its
// Task: trailer names one. The newest is the one a release is read for.
func TestMatchShipped(t *testing.T) {
	rec := func(full, short, subject, trailer string) string {
		return strings.Join([]string{full, short, subject, trailer}, "\x1f") + "\x1e\n"
	}

	log := rec("aaaa", "aa", "FT-533: Make the CLI a client (#553)", "") +
		rec("bbbb", "bb", "FT-99: unrelated (#500)", "") +
		rec("cccc", "cc", "Refresh docs", "FT-531") +
		rec("dddd", "dd", "Fix FT-531 typo", "")

	got, newest := matchShipped(log, "FT", map[string]bool{"FT-531": true, "FT-533": true})
	want := []string{"aa FT-533: Make the CLI a client (#553)", "cc Refresh docs"}

	if !slices.Equal(got.Shipped, want) || newest != "aaaa" {
		t.Errorf("shipped = %q, newest %q", got.Shipped, newest)
	}

	if none, newest := matchShipped(log, "FT", map[string]bool{"FT-7": true}); none.Shipped != nil || newest != "" {
		t.Errorf("a task nothing names shipped %q", none.Shipped)
	}
	// a follow-up PR's commit names its task by Change: alone
	change := strings.Join([]string{"eeee", "ee", "Tidy the docs", "", "FT-9.2"}, "\x1f") + "\x1e\n"
	if got, _ := matchShipped(change, "FT", map[string]bool{"FT-9": true}); len(got.Shipped) != 1 {
		t.Errorf("a Change: FT-9.2 commit shipped %q, want it counted for FT-9", got.Shipped)
	}
}

// A start becomes a discussion when every task is closed with no branch, or
// when it lists a bundle with a closed member. A failed read counts as open,
// and so does a driver that states no state.
func TestClosedStart(t *testing.T) {
	state := map[string]tasksource.TaskInfo{
		"GH-1": {Closed: true, Stated: true},
		"GH-2": {Stated: true},
		"GH-3": {Closed: true},
		"GH-4": {Closed: true, Stated: true},
	}

	c := closedCheck{
		tr: stack.Tracker{
			Resolve: func(_ context.Context, id string) (ref.Ref, error) {
				if id == "GH-9" {
					return ref.Ref{}, errors.New("unknown")
				}

				if id == "GH-5" {
					return ref.Ref{ID: "GH-5", Bundle: []string{"GH-5", "GH-1"}}, nil
				}

				return ref.Ref{ID: id}, nil
			},
			Info: func(_ context.Context, r ref.Ref) (tasksource.TaskInfo, error) {
				return state[r.ID], nil
			},
		},
		branched: func(_ context.Context, id string) bool { return id == "GH-4" },
	}

	for _, tc := range []struct {
		ids  []string
		want []string
	}{
		{[]string{"GH-1"}, []string{"GH-1"}},
		{[]string{"GH-5"}, []string{"GH-5+GH-1"}},
		{[]string{"GH-1", "GH-2"}, nil},
		{[]string{"GH-5", "GH-2"}, []string{"GH-5+GH-1"}},
		{[]string{"GH-3"}, nil},
		{[]string{"GH-4"}, nil},
		{[]string{"GH-9"}, nil},
		{nil, nil},
	} {
		if got := c.all(t.Context(), tc.ids); !slices.Equal(got, tc.want) {
			t.Errorf("all(%v) = %v, want %v", tc.ids, got, tc.want)
		}
	}
}

func TestSwitchNote(t *testing.T) {
	if got := SwitchNote([]string{"GH-1"}, []string{"--plan"}, ""); got !=
		"GH-1 is closed: opening a discussion instead of running the flow (--plan dropped)" {
		t.Errorf("one = %q", got)
	}

	if got := SwitchNote([]string{"GH-1", "GH-2"}, nil, ""); !strings.HasPrefix(got, "GH-1, GH-2 are closed:") {
		t.Errorf("two = %q", got)
	}

	if got := SwitchNote([]string{"GH-1"}, nil, "opus"); got !=
		"GH-1 is closed: opening a discussion instead of running the flow, on the opus profile" {
		t.Errorf("profile = %q", got)
	}
}

// A closed flow start keeps its base and the first task's :profile as the
// discussion's agent, and names the flags and other profiles it drops.
func TestSwitchKeepsBaseAndProfile(t *testing.T) {
	a, err := drivectl.ParseDriveArgs([]string{"GH-1:opus", "GH-2:sonnet", "--plan", "--pace", "fast"})
	if err != nil {
		t.Fatal(err)
	}

	if got := switchProfile(a); got != "opus" {
		t.Errorf("profile = %q, want opus", got)
	}

	if got := switchDropped(a); !slices.Equal(got, []string{"--plan", "--pace fast", "GH-2:sonnet"}) {
		t.Errorf("dropped = %q", got)
	}

	a, err = drivectl.ParseDriveArgs([]string{"GH-1:opus", "--agent", "haiku", "--after", "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if got := switchProfile(a); got != "" {
		t.Errorf("profile = %q, want none beside the run-wide haiku", got)
	}

	// the daemon's drive process never switches: its caller already checked
	t.Setenv(daemon.ResumeEnv, "1")

	if sw := ClosedSwitch(t.Context(), nil, a); sw != nil {
		t.Errorf("switched in the drive process: %+v", sw)
	}
}
