package stack

import "github.com/amberpixels/f10/cli/internal/gitx"

func WorktreeOf(wts []gitx.Worktree, branch string) string {
	for _, wt := range wts {
		if wt.Branch == branch {
			return wt.Path
		}
	}

	return ""
}
