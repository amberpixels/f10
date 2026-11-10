package worktree

import (
	"context"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
)

// TitleLookup asks the host for the task's title, needed only when no driver
// names the branch. A checkout with no host CLI answers with the reason, so
// the note beneath the report can quote it.
func TitleLookup(t *target.Target, number string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		return hostops.TasksFor(t).Title(ctx, ref.Ref{Number: number})
	}
}
