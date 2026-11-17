package drive

import (
	"slices"
	"strings"
	"testing"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// A discussion, --task and a PR url start one agent through the API. Every
// other start is a drive, a bare #n aside. No case here reads the host.
func TestOneShotRouting(t *testing.T) {
	for _, c := range []struct {
		argv  []string
		once  bool
		mode  f10v1.StartMode
		note  string
		tasks []string
		err   string
	}{
		{argv: []string{"GH-1"}},
		{argv: []string{"GH-1", "GH-2", "--parallel", "2"}},
		{argv: []string{"GH-1", "--", "GH-4"}},
		{argv: []string{"GH-1", "--plan"}},
		{argv: []string{"GH-1", "--discuss", "judge", "it"}, once: true, mode: f10v1.StartMode_START_MODE_DISCUSS, note: "judge it"},
		{argv: []string{"https://github.com/o/r/pull/9", "check", "tests"}, once: true, note: "check tests"},
		{argv: []string{"https://github.com/o/r/pull/9", "--task", "GH-3"}, once: true},
		{argv: []string{"GH-1", "GH-2", "GH-3+GH-4", "--discuss"}, once: true, mode: f10v1.StartMode_START_MODE_DISCUSS, tasks: []string{"GH-2", "GH-3+GH-4"}},
		{argv: []string{"12", "--discuss", "13", "judge", "GH-2", "first"}, once: true, mode: f10v1.StartMode_START_MODE_DISCUSS, tasks: []string{"13"}, note: "judge GH-2 first"},
		{argv: []string{"GH-1", "--discuss", "--", "GH-4"}, err: "a range"},
		{argv: []string{"https://github.com/o/r/pull/9", "GH-2", "tests"}, once: true, note: "GH-2 tests"},
		{argv: []string{"GH-1", "--discuss", "--parallel", "2"}, err: "--discuss and a PR pickup take ids and a note"},
		{argv: []string{"--discuss"}, err: "needs a task id"},
		{argv: []string{"GH-1", "--discuss", "--plan"}, err: "exclusive"},
	} {
		req, err := oneShot(t.Context(), nil, c.argv)

		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want %q", c.argv, err, c.err)
			}
		case err != nil:
			t.Errorf("%q: %v", c.argv, err)
		case (req != nil) != c.once:
			t.Errorf("%q: one-shot = %v, want %v", c.argv, req != nil, c.once)
		case req != nil && (req.GetMode() != c.mode || req.GetNote() != c.note || !slices.Equal(req.GetTasks(), c.tasks)):
			t.Errorf("%q: mode %v note %q tasks %q, want %v %q %q",
				c.argv, req.GetMode(), req.GetNote(), req.GetTasks(), c.mode, c.note, c.tasks)
		}
	}
}
