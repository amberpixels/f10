package stack

import (
	"context"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
)

func TaskRows(ctx context.Context, t *target.Target, limit int) ([]hostops.Row, error) {
	rows, err := tasksource.TasksFor(t).List(ctx, limit)
	RememberRefsFor(ctx, t, hostops.RowRefs(rows)...)

	return rows, err
}
