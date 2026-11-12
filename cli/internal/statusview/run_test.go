package statusview

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/branchmeta/branchmetatest"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
)

// The gates show what ship runs under the pace the branch records: under
// ultra, plan, judge and cleanup never run, so they are no gates.
func TestGatesFollowTheRecordedPace(t *testing.T) {
	declared := flow.Of(strings.Fields("plan judge implement cleanup pr review:local judge pr"))
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": declared}, PaceSet: facts.NewPaceSet()}
	src := GateSource{Eff: eff, Main: "/code/repo"}

	meta := branchmetatest.Use(t)
	meta.Seed("GH-1", branchmeta.Pace, "ultra")

	if got := src.Pipeline(t.Context(), "default", "GH-1", "").Strings(); strings.Join(got, " ") !=
		"implement pr review:local pr" {
		t.Errorf("under ultra: %v", got)
	}

	if got := src.Pipeline(t.Context(), "default", "GH-2", "").Strings(); strings.Join(got, " ") !=
		"judge implement cleanup pr review:local judge pr" {
		t.Errorf("no record: %v", got)
	}

	// a task a drive has not opened yet takes the drive's --pace
	if got := src.Pipeline(t.Context(), "default", "", "ultra").Strings(); strings.Join(got, " ") !=
		"implement pr review:local pr" {
		t.Errorf("a queued drive task under ultra: %v", got)
	}
}
