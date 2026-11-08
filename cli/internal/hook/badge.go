package hook

import (
	"context"
	"slices"

	"github.com/amberpixels/f10/cli/internal/observe"
	"github.com/amberpixels/f10/cli/internal/state"
)

// badge applies the observations that settle what the model would otherwise
// report itself. It only fills in: a stop state, a named task or a phase the
// model moved past is never overridden, since an async hook can land late.
type badge struct {
	env     *Env
	session string
	cwd     string
}

// planningSteps are loaded before a pipeline runs, never as a pipeline step.
var planningSteps = []string{"capture", "fetch", "plan"}

func (b badge) Observe(ctx context.Context, o observe.Observation) error {
	if b.session == "" {
		return nil
	}

	// the id lookup runs git and reads the project, so it happens before the
	// session's lock is taken, never while holding it
	issueID := ""
	if o, ok := o.(*observe.IssueCreated); ok {
		issueID = o.ID
		if issueID == "" && b.env.TaskID != nil {
			issueID = b.env.TaskID(ctx, b.cwd, o.URL)
		}
	}

	apply := func(r *state.Run, loaded bool) (bool, error) { return b.apply(o, issueID, r, loaded) }

	return state.Update(b.env.StateDir, b.session, hookLockWait, b.env.now(), apply)
}

// apply is one observation's change to the run, under the session's lock.
func (b badge) apply(o observe.Observation, issueID string, r *state.Run, loaded bool) (bool, error) {
	switch o := o.(type) {
	case *observe.PlanWritten:
		// the plan file is the plan step's output, so writing one marks plan
		// done unreported
		r.PlanWritten(o.Task)
	case *observe.StepStarted:
		// ship loads each pipeline step alone, right before it runs
		if !loaded || r.Skill != "ship" || len(o.Steps) != 1 || slices.Contains(planningSteps, o.Steps[0]) ||
			(r.Ship != "pending" && r.Ship != "running") {
			return false, nil
		}

		if err := r.Set("ship", "running", o.Steps[0]); err != nil {
			return false, err
		}
	case *observe.IssueCreated:
		// capture creating the task; the first issue (a parent) wins over the
		// subtasks created after it
		if !loaded || r.Capture != "running" || r.Task != "" || issueID == "" {
			return false, nil
		}

		if err := r.SetTask(issueID, o.URL); err != nil {
			return false, err
		}
	default:
		return false, nil
	}

	return true, nil
}
