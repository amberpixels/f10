//go:build unix

package lookaround

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// A finish record outlives a killed drive; only a held lease keeps it.
func TestDropDeadFinish(t *testing.T) {
	root := t.TempDir()
	rec := &ref.DriveFinish{State: ref.FinishRunning, Action: "merging #9"}

	branches := map[string]Branch{
		"GH-1/a":      {Task: "GH-1", Finish: rec},
		"GH-2/b":      {Finish: rec}, // its task spelled by its name
		"GH-3+GH-4/c": {Finish: rec}, // a bundle's lead
		"GH-5/d":      {Task: "GH-5", Finish: rec},
	}

	for _, id := range []string{"GH-1", "GH-2", "GH-3"} {
		l, err := drives.Acquire(root, id, 1)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(l.Release)
	}

	dropDeadFinish(branches, root, "GH")

	for name, kept := range map[string]bool{"GH-1/a": true, "GH-2/b": true, "GH-3+GH-4/c": true, "GH-5/d": false} {
		if got := branches[name].Finish != nil; got != kept {
			t.Errorf("%s: record kept = %v, want %v", name, got, kept)
		}
	}

	dropDeadFinish(branches, "", "GH")

	if branches["GH-1/a"].Finish != nil {
		t.Error("a record kept with no storage root to probe")
	}
}
