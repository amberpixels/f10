package task

import (
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
)

// refusedUpFront is the refusal a write gives before stdin is read; a
// driver may still take the verb, so only a source with no driver refuses.
func refusedUpFrontFor(t *target.Target) (tasksource.RefusedSource, bool) {
	s, ok := tasksource.TasksFor(t).(tasksource.RefusedSource)

	return s, ok
}
