package statusview

import (
	"context"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
)

func BranchAfter(ctx context.Context, dir, branch string) string {
	return branchmeta.Out(ctx, dir, branch, branchmeta.After)
}
