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

// AgentKeyDefault is the key the task's agent runs on.
const AgentKeyDefault = "default"

// CheckAgentKey reports why key is no agents key: not `default`, not a step
// some flow names, or a kind scope outside TaskKinds. steps are the step
// names every flow names, merged; a map read outside a project (the
// user's file) passes nil and keeps any step word.
func CheckAgentKey(key string, steps []string) error {
	step, kind, scoped := strings.Cut(key, ".")

	switch {
	case key == AgentKeyDefault:
		return nil
	case step == "" || scoped && kind == "":
		return fmt.Errorf("agents.%s: a key is default, a step, or step.kind", key)
	case scoped && !slices.Contains(TaskKinds, kind):
		return fmt.Errorf("agents.%s: %s is not a task kind (%s)", key, kind, strings.Join(TaskKinds, ", "))
	case steps != nil && !slices.Contains(steps, step):
		return fmt.Errorf("agents.%s: no flow has a %s entry (f10 flow lists them)", key, step)
	}

	return nil
}

// FlowSteps is every step the flows name, each once, sorted.
func (e *Effective) FlowSteps() []string {
	seen := map[string]bool{}

	for _, entries := range e.Flows {
		for _, entry := range entries {
			step, _ := SplitEntry(entry)
			seen[step] = true
		}
	}

	return slices.Sorted(maps.Keys(seen))
}

// resolveAgents records the merged agents map once the flows are known,
// since a key is a step a flow names.
func (e *Effective) resolveAgents(fm *layered) {
	if fm.Agents == nil {
		return
	}

	steps := e.FlowSteps()
	e.Agents = map[string]string{}
	e.AgentsOrigin = map[string]string{}

	layers := map[string]bool{}

	for _, key := range slices.Sorted(maps.Keys(fm.Agents)) {
		if err := CheckAgentKey(key, steps); err != nil {
			e.problem(true, "project.md frontmatter: %v", err)
		}

		e.Agents[key] = fm.Agents[key]
		e.AgentsOrigin[key] = "declared (" + fm.origin["agents."+key] + ", frontmatter)"
		layers[fm.origin["agents."+key]] = true
	}

	e.Facts = append(e.Facts, Fact{
		Key:    "agents",
		Value:  mapShown(e.Agents),
		Origin: "declared (" + strings.Join(slices.Sorted(maps.Keys(layers)), " + ") + ", frontmatter)",
	})
}
