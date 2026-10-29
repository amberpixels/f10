package main

import (
	"context"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// baseHasCode reports a non-merge commit on a base task's branch past the
// default branch, its own recorded base and its creation commit: a merge is a
// catchup's, never code. The drive and ship's guard both decide by it, on
// local refs, so a branch never pushed counts.
func baseHasCode(ctx context.Context, main, branch, def string) bool {
	args := []string{"rev-list", "--count", "--no-merges", "refs/heads/" + branch, "--not", "refs/heads/" + def}

	// a ref git cannot read would drop out of the --not list and count the
	// base's own base as its commits, so it releases nobody this poll
	fork, err := gitx.Config(ctx, main, "branch."+branch+"."+cfgAfterBranch)
	if err != nil {
		return false
	}

	for _, r := range []string{"refs/remotes/origin/" + def, "refs/heads/" + fork} {
		if strings.HasSuffix(r, "/") {
			continue
		}

		sha, err := gitx.Ref(ctx, main, r)
		if err != nil {
			return false
		}

		if sha != "" {
			args = append(args, r)
		}
	}

	if created := createdAt(ctx, main, branch); created != "" {
		args = append(args, created)
	}

	n := gitx.Out(ctx, main, args...)

	return n != "" && n != "0"
}
