package config

import (
	"maps"
	"slices"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// agentReach describes what runs on each spawning step's key, and facts
// accepts those keys without a flow entry, so the two must name one set.
func TestAgentReachMatchesSpawningSteps(t *testing.T) {
	t.Parallel()

	var reached []string

	for key := range maps.Keys(agentReach) {
		if key != facts.AgentKeyDefault {
			reached = append(reached, key)
		}
	}

	got := slices.Sorted(slices.Values(reached))
	if want := slices.Sorted(slices.Values(facts.SpawningSteps)); !slices.Equal(got, want) {
		t.Errorf("agentReach spawners %v, want facts.SpawningSteps %v", got, want)
	}
}
