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
	// load: a writer call landing during it must not be saved over
	issueID := ""
	if o, ok := o.(*observe.IssueCreated); ok {
		issueID = o.ID
		if issueID == "" && b.env.TaskID != nil {
			issueID = b.env.TaskID(ctx, b.cwd, o.URL)
		}
	}

	r, loaded := state.LoadOrFresh(b.env.StateDir, b.session)

	switch o := o.(type) {
	case *observe.PlanWritten:
		// the plan file is the plan step's output, so writing one marks plan
		// done unreported
		r.PlanWritten(o.Task)
	case *observe.StepStarted:
		// ship loads each pipeline step alone, right before it runs
		if !loaded || r.Skill != "ship" || len(o.Steps) != 1 || slices.Contains(planningSteps, o.Steps[0]) ||
			(r.Ship != "pending" && r.Ship != "running") {
			return nil
		}

		if err := r.Set("ship", "running", o.Steps[0]); err != nil {
			return err
		}
	case *observe.IssueCreated:
		// capture creating the task; the first issue (a parent) wins over the
		// subtasks created after it
		if !loaded || r.Capture != "running" || r.Task != "" || issueID == "" {
			return nil
		}

		if err := r.SetTask(issueID, o.URL); err != nil {
			return err
		}
	default:
		return nil
	}

	return r.Save(b.env.StateDir, b.env.now())
}
