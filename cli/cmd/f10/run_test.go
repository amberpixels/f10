package main

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/branchmeta/branchmetatest"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
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
		{want: "medium", origin: paceOriginDefault},
	} {
		meta := branchmetatest.Use(t)
		meta.Seed("GH-1", branchmeta.Pace, tc.recorded)

		eff := &facts.Effective{PaceSet: facts.NewPaceSet()}
		if tc.def != "" {
			eff.PaceSet.Default, eff.PaceSet.DefaultOrigin = tc.def, facts.DeclaredByUser("/u/paces.md")
		}

		run, err := resolveRun(t.Context(), eff, runInput{pace: tc.flag, main: "/code/repo", branch: "GH-1"})
		if err != nil || run.pace.name != tc.want || run.pace.origin != tc.origin {
			t.Errorf("%+v: pace %+v, %v", tc, run, err)
		}
	}

	eff := &facts.Effective{PaceSet: facts.NewPaceSet()}
	if _, err := resolveRun(t.Context(), eff, runInput{pace: "brisk"}); err == nil ||
		!strings.Contains(err.Error(), "the paces are fast, medium, slow, ultra") {
		t.Errorf("an unknown pace = %v", err)
	}
}

// The gates show what ship runs under the pace the branch records: under
// ultra, plan, judge and cleanup never run, so they are no gates.
func TestGatesFollowTheRecordedPace(t *testing.T) {
	declared := flow.Of(strings.Fields("plan judge implement cleanup pr review:local judge pr"))
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": declared}, PaceSet: facts.NewPaceSet()}
	src := gateSource{eff: eff, main: "/code/repo"}

	meta := branchmetatest.Use(t)
	meta.Seed("GH-1", branchmeta.Pace, "ultra")

	if got := src.pipeline(t.Context(), "default", "GH-1", "").Strings(); strings.Join(got, " ") !=
		"implement pr review:local pr" {
		t.Errorf("under ultra: %v", got)
	}

	if got := src.pipeline(t.Context(), "default", "GH-2", "").Strings(); strings.Join(got, " ") !=
		"judge implement cleanup pr review:local judge pr" {
		t.Errorf("no record: %v", got)
	}

	// a task a drive has not opened yet takes the drive's --pace
	if got := src.pipeline(t.Context(), "default", "", "ultra").Strings(); strings.Join(got, " ") !=
		"implement pr review:local pr" {
		t.Errorf("a queued drive task under ultra: %v", got)
	}
}

// A problem in the user's file names that file, so only the project's own
// errors take the project.md label.
func TestLabeledErrors(t *testing.T) {
	got := labeledErrors("/home/me/.f10/agents.md: agents.review: bad\nagents.x: no flow has an x entry")
	want := "/home/me/.f10/agents.md: agents.review: bad\nproject.md: agents.x: no flow has an x entry"

	if got != want {
		t.Errorf("labeled = %q, want %q", got, want)
	}
}
