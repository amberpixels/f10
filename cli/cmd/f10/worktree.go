package main

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// A task's worktree: created by start, removed by finish, through worktrunk
// when it is on PATH so a project's worktrunk hooks keep firing, else with
// plain git at the sibling path worktrunk would have chosen. The decision
// is made once, here, so the two verbs cannot pick differently.

// worktrunk reports whether `wt` is on PATH.
func worktrunk() bool {
	return shell.Has("wt")
}

// worktreeOf is the path of the worktree with branch checked out, or "".
func worktreeOf(wts []gitx.Worktree, branch string) string {
	for _, wt := range wts {
		if wt.Branch == branch {
			return wt.Path
		}
	}

	return ""
}

// checkout creates the worktree for branch and returns its path, read
// back from git rather than predicted.
func checkout(ctx context.Context, main, branch, base string, create bool) (string, error) {
	if worktrunk() {
		if err := wtSwitch(ctx, main, branch, base, create); err != nil {
			return "", err
		}
	} else {
		if create && base == "" {
			base = defaultBranch(ctx, main)
		}

		if err := gitx.AddWorktree(ctx, main, siblingPath(main, branch), branch, base, create); err != nil {
			return "", err
		}
	}

	wts, err := gitx.Worktrees(ctx, main)
	if err != nil {
		return "", err
	}

	path := worktreeOf(wts, branch)
	if path == "" {
		return "", fmt.Errorf("no worktree for %s after creating it", branch)
	}

	return path, nil
}

// wtSwitch is worktrunk's create-or-open. The flags keep it non-interactive:
// no shell to cd, no approval prompt, structured output nobody has to parse
// because git is asked for the path afterwards.
func wtSwitch(ctx context.Context, main, branch, base string, create bool) error {
	args := []string{"switch", "--no-cd", "--yes", "--format", "json"}

	if create {
		args = append(args, "--create")
		if base != "" {
			args = append(args, "--base", base)
		}
	}

	return wt(ctx, main, append(args, branch)...)
}

// removeCheckout removes the worktree and deletes its branch. The branch
// goes with -D: the host confirmed the merge, which a squash or rebase
// hides from git.
func removeCheckout(ctx context.Context, main, path, branch string) error {
	if worktrunk() {
		return wt(ctx, main, "remove", "--yes", "--force-delete", "--foreground", "--format", "json", branch)
	}

	if err := gitx.RemoveWorktree(ctx, main, path); err != nil {
		return err
	}

	return gitx.DeleteBranch(ctx, main, branch)
}

// wt runs one worktrunk command in main. A non-zero exit is worktrunk's
// own words.
func wt(ctx context.Context, main string, args ...string) error {
	res, err := shell.Capture(ctx, main, "wt", args...)
	if err != nil {
		return fmt.Errorf("running wt %s: %w", args[0], err)
	}

	if res.Code != 0 {
		return fmt.Errorf("wt %s: %s", strings.Join(args, " "),
			cmp.Or(res.Stderr, res.Stdout, fmt.Sprintf("exit %d", res.Code)))
	}

	return nil
}

// siblingPath is the worktree layout worktrunk produces and per-project
// cleanup scripts already scan: beside the main checkout, named after it
// and the branch, slashes flattened to dashes.
func siblingPath(main, branch string) string {
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+"."+strings.ReplaceAll(branch, "/", "-"))
}
