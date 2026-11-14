package hook

import (
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The profile is the one the cwd's branch records, read within the hook's
// time budget; a branch with none, or no repo, gives the zero profile.
func TestHookProfileReadsTheBranchRecord(t *testing.T) {
	dir := t.TempDir()
	testkit.InitRepo(t, dir, "FT-12/thing")

	if got := hookProfile(t.Context(), dir); got != (agentctl.Profile{}) {
		t.Errorf("no record read %+v", got)
	}

	want := agentctl.Profile{Kind: "claude", Model: "opus", Effort: "high"}
	if err := branchmeta.Set(t.Context(), dir, "FT-12/thing", branchmeta.Agent, want.String()); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	got := hookProfile(t.Context(), dir)

	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("profile read took %v", took)
	}

	if got.Kind != "claude" || got.Effort != "high" || got.Model == "" {
		t.Errorf("profile %+v, recorded %q", got, want.String())
	}

	if got := hookProfile(t.Context(), t.TempDir()); got != (agentctl.Profile{}) {
		t.Errorf("outside a repo %+v", got)
	}
}
