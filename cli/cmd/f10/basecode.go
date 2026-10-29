package main

import (
	"context"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// baseHasCode reports a non-merge commit on a base task's branch past the
// default branch, its own recorded base and its creation commit: a merge is a
// catchup's, never code. The drive and ship's guard both decide by it, on
// local refs, so a branch never pushed counts.
func baseHasCode(ctx context.Context, main, branch, def string) bool {
	return hasCodeAt(ctx, main, "refs/heads/"+branch, branch, def, createdAt(ctx, main, branch))
}

// originBaseHasCode is baseHasCode for a base only origin holds: with no
// reflog there is no creation commit, so the base's own base is read on
// origin too.
func originBaseHasCode(ctx context.Context, main, branch, def string) bool {
	return hasCodeAt(ctx, main, "refs/remotes/origin/"+branch, branch, def, "")
}

func hasCodeAt(ctx context.Context, main, tip, branch, def, created string) bool {
	args := []string{"rev-list", "--count", "--no-merges", tip, "--not", "refs/heads/" + def}

	// a ref git cannot read would drop out of the --not list and count the
	// base's own base as its commits, so it releases nobody this poll
	fork, err := gitx.Config(ctx, main, "branch."+branch+"."+cfgAfterBranch)
	if err != nil {
		return false
	}

	refs := []string{"refs/remotes/origin/" + def}
	if fork != "" {
		refs = append(refs, "refs/heads/"+fork, "refs/remotes/origin/"+fork)
	}

	for _, r := range refs {
		sha, err := gitx.Ref(ctx, main, r)
		if err != nil {
			return false
		}

		if sha != "" {
			args = append(args, r)
		}
	}

	if created != "" {
		args = append(args, created)
	}

	n := gitx.Out(ctx, main, args...)

	return n != "" && n != "0"
}
