package finish

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The janitor keeps a branch whose upstream git cannot read, and says why.
func TestJanitorKeepsABranchItCannotRead(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Fail("git config --get branch.GH-1.merge", testkit.Broken)

	j := &Sweeper{In: JanitorInput{Main: "/code/repo"}, Def: "main"}

	if _, removed := j.SweepUnpushed(t.Context(), staleBranch{Name: "GH-1", LocalSHA: "abc"}); removed {
		t.Fatal("removed a branch whose upstream could not be read")
	}

	if len(j.Lines) != 1 || !strings.Contains(j.Lines[0], "GH-1 kept") {
		t.Errorf("lines = %q, want one naming the kept branch", j.Lines)
	}

	if f.Called("git branch -D") {
		t.Errorf("deleted the branch: %q", f.Calls)
	}
}
