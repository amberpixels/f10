package drivectl_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/launch"
)

// A discussion, --task and a PR url start one agent through the API. Every
// other start is a drive, and a bare #n is the host's to name.
func TestParseStart(t *testing.T) {
	for _, c := range []struct {
		argv  []string
		kind  drivectl.StartKind
		mode  string
		note  string
		tasks []string
		err   string
	}{
		{argv: []string{"GH-1"}},
		{argv: []string{"GH-1", "GH-2", "--parallel", "2"}},
		{argv: []string{"GH-1", "--", "GH-4"}},
		{argv: []string{"GH-1", "--plan"}},
		{argv: []string{"#12"}, kind: drivectl.MaybePickup},
		{argv: []string{"GH-1", "--discuss", "judge", "it"}, kind: drivectl.DiscussStart, mode: launch.ModeDiscuss, note: "judge it"},
		{argv: []string{"https://github.com/o/r/pull/9", "check", "tests"}, kind: drivectl.PickupStart, note: "check tests"},
		{argv: []string{"https://github.com/o/r/pull/9", "--task", "GH-3"}, kind: drivectl.PickupStart},
		{argv: []string{"GH-1", "GH-2", "GH-3+GH-4", "--discuss"}, kind: drivectl.DiscussStart, mode: launch.ModeDiscuss, tasks: []string{"GH-2", "GH-3+GH-4"}},
		{argv: []string{"12", "--discuss", "13", "judge", "GH-2", "first"}, kind: drivectl.DiscussStart, mode: launch.ModeDiscuss, tasks: []string{"13"}, note: "judge GH-2 first"},
		{argv: []string{"GH-1", "--discuss", "--", "GH-4"}, err: "a range"},
		{argv: []string{"https://github.com/o/r/pull/9", "GH-2", "tests"}, kind: drivectl.PickupStart, note: "GH-2 tests"},
		{argv: []string{"GH-1", "--discuss", "--parallel", "2"}, err: "--discuss and a PR pickup take ids and a note"},
		{argv: []string{"--discuss"}, err: "needs a task id"},
		{argv: []string{"GH-1", "--discuss", "--plan"}, err: "exclusive"},
		{argv: []string{"GH-1", "--after", "GH-2", "--base", "main"}, err: "exclusive"},
		{argv: []string{"GH-1", "--task"}, err: "needs a value"},
	} {
		sa, err := drivectl.ParseStart(c.argv)

		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want %q", c.argv, err, c.err)
			}
		case err != nil:
			t.Errorf("%q: %v", c.argv, err)
		case sa.Kind != c.kind:
			t.Errorf("%q: kind = %v, want %v", c.argv, sa.Kind, c.kind)
		case sa.Kind != drivectl.FlowStart &&
			(sa.Once.Mode != c.mode || sa.Once.Note != c.note || !slices.Equal(sa.Once.Tasks, c.tasks)):
			t.Errorf("%q: mode %q note %q tasks %q, want %q %q %q",
				c.argv, sa.Once.Mode, sa.Once.Note, sa.Once.Tasks, c.mode, c.note, c.tasks)
		}
	}

	// a flow start's own refusals are the drive's
	if _, err := drivectl.ParseStart([]string{"GH-1", "--parallel", "x"}); err == nil {
		t.Error("a bad --parallel parsed")
	}

	// a flow start carries its drive arguments, and a MaybePickup reads as one
	sa, err := drivectl.ParseStart([]string{"GH-1", "--pace", "fast"})
	if err != nil || sa.Drive.Pace != "fast" {
		t.Errorf("flow = %+v, %v", sa.Drive, err)
	}

	sa, _ = drivectl.ParseStart([]string{"#12", "--plan"})
	if a, err := sa.Flow(); err != nil || !a.PlanOnly || a.Tasks[0] != "#12" {
		t.Errorf("#12 as a flow = %+v, %v", a, err)
	}
}

// Every flag a discussion takes parses there, and every other flag sends the
// command line to the drive.
func TestOnceFlagsParse(t *testing.T) {
	for _, f := range drivectl.StartFlags {
		argv := []string{"GH-1", "--discuss", f.Name}
		if f.Value != "" {
			argv = append(argv, "x")
		}

		if f.Name == "--discuss" || f.Name == "--plan" || f.Name == "--flow" || f.Name == "--local" {
			continue // exclusive with --discuss, or the flag itself
		}

		_, err := drivectl.ParseStart(argv)
		if f.Once && err != nil {
			t.Errorf("%s: %v, want a discussion to take it", f.Name, err)
		}

		if !f.Once && err == nil {
			t.Errorf("%s: a discussion took a flow start's flag", f.Name)
		}
	}
}
