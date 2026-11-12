package hostops_test

import (
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// An archived spike report counts the task finished, as a plan does.
func TestFinishedTaskBySpikeReport(t *testing.T) {
	root := storage.Root(t.TempDir())
	testkit.WritePlan(t, filepath.Join(root.Archive(), "GH-1.spike.md"))

	if !stack.FinishedTask(hostops.TaskInfo{}, root, "GH-1", false) {
		t.Error("an archived spike report did not count")
	}
}
