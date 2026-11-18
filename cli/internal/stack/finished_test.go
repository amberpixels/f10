package stack_test

import (
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// One rule decides a finished task for drive, status, children and drive
// list: no branch, no live plan, then the tracker's state where it states
// one, else the archive.
func TestFinishedTask(t *testing.T) {
	cases := []struct {
		name               string
		closed, stated     bool
		branched           bool
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
		{name: "open (stated), archived", stated: true, archived: true},
		{name: "open (stated), nothing", stated: true},
		{name: "closed (stated), archived", closed: true, stated: true, archived: true, want: true},
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

			if got := stack.FinishedTask(
				tasksource.TaskInfo{Closed: c.closed, Stated: c.stated},
				root,
				"GH-1",
				c.branched,
			); got != c.want {
				t.Errorf("finishedTask = %v, want %v", got, c.want)
			}
		})
	}
}

// An archived spike report counts the task finished, as a plan does.
func TestFinishedTaskBySpikeReport(t *testing.T) {
	root := storage.Root(t.TempDir())
	testkit.WritePlan(t, filepath.Join(root.Archive(), "GH-1.spike.md"))

	if !stack.FinishedTask(tasksource.TaskInfo{}, root, "GH-1", false) {
		t.Error("an archived spike report did not count")
	}
}
