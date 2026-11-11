package facts

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// TaskKinds are the kinds a step key may be scoped by: `plan.bug` sets the
// plan step's profile for a bug. The words are the plan's Kind line.
var TaskKinds = []string{"feature", "bug", "refactor", "spike"}

// StandaloneSpawners are the steps that spawn subagents outside any flow, so
// their agents key needs no flow entry: the audit's reviewers, judge --blind.
var StandaloneSpawners = []string{"audit", "judge"}

// AgentKeyDefault is the key the task's agent runs on.
const AgentKeyDefault = "default"

// CheckAgentKey reports why key is no agents key: not `default`, not a step
// some flow names or a standalone spawner, or a kind scope outside
// TaskKinds. steps are the step names every flow names, merged; a map read
// outside a project (the user's file) passes nil and keeps any step word.
func CheckAgentKey(key string, steps []string) error {
	step, kind, scoped := strings.Cut(key, ".")

	switch {
	case key == AgentKeyDefault:
		return nil
	case step == "" || scoped && kind == "" || strings.HasPrefix(key, "-"):
		// a key starting with - is a run's flag, which only a command line gives
		return fmt.Errorf("agents.%s: a key is default, a step, or step.kind", key)
	case scoped && !slices.Contains(TaskKinds, kind):
		return fmt.Errorf("agents.%s: %s is not a task kind (%s)", key, kind, strings.Join(TaskKinds, ", "))
	case steps != nil && !slices.Contains(steps, step) && !slices.Contains(StandaloneSpawners, step):
		return fmt.Errorf("agents.%s: no flow has a %s entry (f10 flow lists them)", key, step)
	}

	return nil
}

// FlowSteps is every step the flows name, each once, sorted.
func (e *Effective) FlowSteps() []string {
	seen := map[string]bool{}

	for _, entries := range e.Flows {
		for _, entry := range entries {
			seen[entry.Step] = true
		}
	}

	return slices.Sorted(maps.Keys(seen))
}

// resolveAgents records the merged agents map once the flows are known,
// since a project's key is a step a flow names; the user's file serves
// every project, so its keys were checked without the flows.
func (e *Effective) resolveAgents(fm *layered) {
	if fm.Agents == nil {
		return
	}

	steps := e.FlowSteps()
	e.Agents = map[string]string{}
	e.AgentsOrigin = map[string]Origin{}

	for _, key := range slices.Sorted(maps.Keys(fm.Agents)) {
		origin := fm.originOf("agents." + key)

		if origin.Layer != userLayer {
			if err := CheckAgentKey(key, steps); err != nil {
				e.problem(true, "project.md frontmatter: %v", err)
			}
		}

		e.Agents[key] = fm.Agents[key]
		e.AgentsOrigin[key] = origin
		e.Facts = append(e.Facts, Fact{Key: "agents." + key, Value: e.Agents[key], Origin: origin})
	}
}
