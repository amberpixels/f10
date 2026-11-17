package start

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
)

// settleDiscussion reads what a discussion covers: the listed tasks, and
// which of them have subtasks. A lone task with none discusses as before,
// so it gets nil. Every listed id resolves before anything is created.
func settleDiscussion(
	ctx context.Context,
	t *target.Target,
	r ref.Ref,
	pick *hostops.Pickup,
	req Request,
) (*launch.Discussion, error) {
	if req.Mode != launch.ModeDiscuss {
		if len(req.Also) > 0 {
			return nil, errors.New(
				"several ids go to one agent only with --discuss; a flow start takes them as a drive",
			)
		}

		return nil, nil
	}

	if len(req.Also) > 0 {
		switch {
		case pick != nil:
			return nil, errors.New("a PR is picked up alone: --discuss over several tasks takes task ids")
		case strings.TrimSpace(req.After) != "" || strings.TrimSpace(req.Base) != "":
			return nil, errors.New("--after and --base name one task's base: a discussion of several tasks has none")
		}
	}

	if pick != nil {
		return nil, nil
	}

	refs := []ref.Ref{r}

	for _, token := range req.Also {
		id, _ := ref.SplitSuffix(strings.TrimSpace(token))

		other, err := t.Reference(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", token, err)
		}

		refs = append(refs, other.Head())
	}

	d := launch.Discussion{}
	tr := stack.TrackerFor(t)

	for _, x := range refs {
		d.Tasks = append(d.Tasks, x.Label())

		parent, err := hasSubtasks(ctx, tr, x)
		if err != nil {
			return nil, err
		}

		if parent {
			d.Parents = append(d.Parents, x.ID)
		}
	}

	if len(d.Tasks) == 1 && len(d.Parents) == 0 {
		return nil, nil
	}

	return &d, nil
}

// hasSubtasks says whether the task is a parent; a tracker with no parent
// link has none.
func hasSubtasks(ctx context.Context, tr stack.Tracker, r ref.Ref) (bool, error) {
	if tr.Children == nil || len(r.Riders()) > 0 {
		return false, nil
	}

	rows, err := tr.Children(ctx, r)
	if errors.Is(err, tasksource.ErrNoParentLink) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("%s: reading its subtasks: %w", r.ID, err)
	}

	return len(rows) > 0, nil
}

// discussed is what a discussion's branch records: the ids it opened for.
func discussed(in StartInput) string {
	if in.Discussion != nil {
		return strings.Join(in.Discussion.Tasks, " ")
	}

	return in.Task.Label()
}

// discussedLabels is every task the discussion reads, the worktree's first.
func discussedLabels(r ref.Ref, d *launch.Discussion) []string {
	if d != nil {
		return d.Tasks
	}

	return []string{r.Label()}
}
