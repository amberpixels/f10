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

// A task's worktree goes through worktrunk when `wt` is on PATH, so a
// project's worktrunk hooks keep firing, else plain git at the sibling path
// worktrunk would choose. Decided once, here, so start and finish agree.

func worktrunk() bool {
	return shell.Has("wt")
}

func worktreeOf(wts []gitx.Worktree, branch string) string {
	for _, wt := range wts {
		if wt.Branch == branch {
			return wt.Path
		}
	}

	return ""
}

// checkout reads the new worktree's path back from git, never predicted.
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

// wtSwitch's flags keep worktrunk non-interactive: no shell to cd, no
// approval prompt, and output nobody parses, since git is asked for the path.
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

// removeCheckout deletes the branch with -D: the host confirmed the merge,
// which a squash or rebase hides from git.
func removeCheckout(ctx context.Context, main, path, branch string) error {
	if worktrunk() {
		return wt(ctx, main, "remove", "--yes", "--force-delete", "--foreground", "--format", "json", branch)
	}

	if err := gitx.RemoveWorktree(ctx, main, path); err != nil {
		return err
	}

	return gitx.DeleteBranch(ctx, main, branch)
}

// wt reports a non-zero exit in worktrunk's own words.
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

// siblingPath matches worktrunk's layout, which per-project cleanup scripts
// already scan.
func siblingPath(main, branch string) string {
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+"."+strings.ReplaceAll(branch, "/", "-"))
}
