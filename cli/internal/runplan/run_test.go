package runplan_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/branchmeta/branchmetatest"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/runplan"
)

// A run's pace is the flag, else the branch's record, else the default a
// layer names, else medium.
func TestResolveRunPaceOrder(t *testing.T) {
	for _, tc := range []struct {
		flag, recorded, def, want, origin string
	}{
		{flag: "Fast", recorded: "slow", def: "ultra", want: "fast", origin: "--pace"},
		{recorded: "slow", def: "ultra", want: "slow", origin: "recorded on branch GH-1"},
		{def: "ultra", want: "ultra", origin: "declared (user, /u/paces.md)"},
		{want: "medium", origin: runplan.PaceOriginDefault},
	} {
		meta := branchmetatest.Use(t)
		meta.Seed("GH-1", branchmeta.Pace, tc.recorded)

		eff := &facts.Effective{PaceSet: facts.NewPaceSet()}
		if tc.def != "" {
			eff.PaceSet.Default, eff.PaceSet.DefaultOrigin = tc.def, facts.DeclaredByUser("/u/paces.md")
		}

		run, err := runplan.ResolveRun(
			t.Context(),
			eff,
			runplan.RunInput{Pace: tc.flag, Main: "/code/repo", Branch: "GH-1"},
		)
		if err != nil || run.Pace.Name != tc.want || run.Pace.Origin != tc.origin {
			t.Errorf("%+v: pace %+v, %v", tc, run, err)
		}
	}

	eff := &facts.Effective{PaceSet: facts.NewPaceSet()}
	if _, err := runplan.ResolveRun(t.Context(), eff, runplan.RunInput{Pace: "brisk"}); err == nil ||
		!strings.Contains(err.Error(), "the paces are fast, medium, slow, ultra") {
		t.Errorf("an unknown pace = %v", err)
	}
}
