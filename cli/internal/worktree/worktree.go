// Package worktree is a task's checkout: where its worktree goes, how it is
// made and removed, and the names and pull tokens that point at one.
package worktree

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
)

func worktrunk() bool {
	return shell.Has("wt")
}

// Checkout reads the new worktree's path back from git, never predicted.
func Checkout(ctx context.Context, main, branch, base string, create bool) (string, error) {
	if worktrunk() {
		if err := wtSwitch(ctx, main, branch, base, create); err != nil {
			return "", err
		}
	} else {
		if create && base == "" {
			base = stack.DefaultBranch(ctx, main)
		}

		if err := gitx.AddWorktree(ctx, main, SiblingPath(main, branch), branch, base, create); err != nil {
			return "", err
		}
	}

	wts, err := gitx.Worktrees(ctx, main)
	if err != nil {
		return "", err
	}

	path := stack.WorktreeOf(wts, branch)
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

// RemoveCheckout deletes the branch with -D: the host confirmed the merge,
// which a squash or rebase hides from git.
func RemoveCheckout(ctx context.Context, main, path, branch string) error {
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

// SiblingPath matches worktrunk's layout, which per-project cleanup scripts
// already scan.
func SiblingPath(main, branch string) string {
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+"."+strings.ReplaceAll(branch, "/", "-"))
}
