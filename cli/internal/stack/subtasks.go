package stack

import (
	"context"
	"errors"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/tasksource"
)

// OpenSubtasks is the check start and drive make before a task runs as one
// of its own.
func OpenSubtasks(ctx context.Context, tr Tracker, r ref.Ref) ([]string, error) {
	if tr.Children == nil {
		return nil, nil
	}

	kids, err := tr.Children(ctx, r)
	if errors.Is(err, tasksource.ErrNoParentLink) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("%s: reading its subtasks: %w", r.ID, err)
	}

	var open []string

	for _, k := range kids {
		if !k.Finished {
			open = append(open, k.ID)
		}
	}

	return open, nil
}
