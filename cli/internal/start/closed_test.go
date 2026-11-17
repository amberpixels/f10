package start

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

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

// A commit ships the task when its subject opens with one of the ids or its
// Task: trailer names one; the newest is the one a release is read for.
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
}

// Only a start whose every task is closed, with no branch, becomes a
// discussion; a read that fails or a driver with no state counts as open.
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
	if got := SwitchNote([]string{"GH-1"}, []string{"--plan"}); got !=
		"GH-1 is closed: opening a discussion instead of running the flow (--plan dropped)" {
		t.Errorf("one = %q", got)
	}

	if got := SwitchNote([]string{"GH-1", "GH-2"}, nil); !strings.HasPrefix(got, "GH-1, GH-2 are closed:") {
		t.Errorf("two = %q", got)
	}
}
