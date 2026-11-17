package drive

import (
	"strings"
	"testing"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
)

// A discussion, --task and a PR url start one agent through the API. Every
// other start is a drive, a bare #n aside. No case here reads the host.
func TestOneShotRouting(t *testing.T) {
	for _, c := range []struct {
		argv []string
		once bool
		mode f10v1.StartMode
		note string
		err  string
	}{
		{argv: []string{"GH-1"}},
		{argv: []string{"GH-1", "GH-2", "--parallel", "2"}},
		{argv: []string{"GH-1", "--", "GH-4"}},
		{argv: []string{"GH-1", "--plan"}},
		{argv: []string{"GH-1", "--discuss", "judge", "it"}, once: true, mode: f10v1.StartMode_START_MODE_DISCUSS, note: "judge it"},
		{argv: []string{"https://github.com/o/r/pull/9", "check", "tests"}, once: true, note: "check tests"},
		{argv: []string{"https://github.com/o/r/pull/9", "--task", "GH-3"}, once: true},
		{argv: []string{"GH-1", "--discuss", "--parallel", "2"}, err: "--discuss and a PR pickup take one id"},
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
		case req != nil && (req.GetMode() != c.mode || req.GetNote() != c.note):
			t.Errorf("%q: mode %v note %q, want %v %q", c.argv, req.GetMode(), req.GetNote(), c.mode, c.note)
		}
	}
}
