package hostops_test

import (
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestAfterIn(t *testing.T) {
	cases := map[string]string{
		"Body.\n\nAfter: GH-12\n":   "GH-12",
		"After:#12":                 "#12",
		"After: GH-12\r\n":          "GH-12",
		"Run after: GH-12":          "",
		"  After: GH-12":            "",
		"After: GH-12 and GH-13\n":  "",
		"no line at all, After: x ": "",
		"**After:** GH-12":          "GH-12",
		"`After:` GH-12":            "GH-12",
		"After: **GH-12**":          "GH-12",
		"After: `GH-12`":            "GH-12",
		"- After: GH-12":            "",
	}

	for body, want := range cases {
		if got := hostops.AfterIn(body); got != want {
			t.Errorf("afterIn(%q) = %q, want %q", body, got, want)
		}
	}
}

// One rule decides a finished task for drive, status, children and drive
// list: no branch, no live plan, and closed or archived.
func TestFinishedTask(t *testing.T) {
	cases := []struct {
		name               string
		closed, branched   bool
		archived, livePlan bool
		want               bool
	}{
		{name: "nothing"},
		{name: "archived", archived: true, want: true},
		{name: "closed", closed: true, want: true},
		{name: "archived, re-planned", archived: true, livePlan: true},
		{name: "closed, re-planned", closed: true, livePlan: true},
		{name: "closed with a branch", closed: true, branched: true},
		{name: "archived with a branch", archived: true, branched: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := storage.Root(t.TempDir())
			if c.archived {
				testkit.WritePlan(t, filepath.Join(root.Archive(), "GH-1.2.md"))
			}

			if c.livePlan {
				testkit.WritePlan(t, root.Plan("GH-1"))
			}

			if got := stack.FinishedTask(hostops.TaskInfo{Closed: c.closed}, root, "GH-1", c.branched); got != c.want {
				t.Errorf("finishedTask = %v, want %v", got, c.want)
			}
		})
	}
}
