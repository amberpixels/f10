package stack

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// BranchDiff is VerifyDiff for the checkout at dir, main the main checkout.
func BranchDiff(ctx context.Context, dir, main string) (string, []string, error) {
	branch := gitx.CurrentBranch(ctx, dir)
	if branch == "" {
		return "", nil, errors.New("HEAD is detached")
	}

	base, err := PRBase(ctx, io.Discard, main, branch)
	if err != nil {
		return "", nil, err
	}

	from, err := ForkPoint(ctx, dir, "HEAD", base)
	if err != nil {
		return base, nil, err
	}

	// a rename lists both paths, and -z keeps non-ASCII paths unquoted
	tracked, err := gitx.Exec(ctx, dir, "diff", "--name-only", "--no-renames", "-z", from)
	if err != nil {
		return base, nil, err
	}

	untracked, err := gitx.Exec(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return base, nil, err
	}

	var files []string

	for _, out := range []string{tracked, untracked} {
		for f := range strings.SplitSeq(out, "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	}

	return base, files, nil
}
