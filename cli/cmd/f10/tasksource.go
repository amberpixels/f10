package main

import (
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/target"
)

// refusedUpFront is the refusal a write gives before stdin is read; a
// driver may still take the verb, so only a source with no driver refuses.
func refusedUpFrontFor(t *target.Target) (hostops.RefusedSource, bool) {
	s, ok := hostops.TasksFor(t).(hostops.RefusedSource)

	return s, ok
}
