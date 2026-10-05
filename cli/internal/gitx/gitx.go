// Package gitx is the binary's one door to git. The reads mirror the
// shell-outs bin/resolve.sh makes - the bash script is the semantics
// oracle, and shelling out identically keeps the two comparable. The
// writes are `f10 start`'s: a branch and a worktree, nothing else.
//
// Everything goes through Exec, a variable so a test can answer git
// without a checkout. No go-git.
package gitx

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Exec runs git with args in dir and returns trimmed stdout. A failure
// carries git's own stderr, which names the cause better than any wrapper
// could. It is a variable so tests can script answers instead of running git.
var Exec = func(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}

		return "", fmt.Errorf("git %s: %w", args[0], err)
	}

	return strings.TrimSpace(string(out)), nil
}

// Out runs git and returns trimmed stdout, or "" on any error - absence is
// a valid result everywhere a read uses it.
func Out(ctx context.Context, dir string, args ...string) string {
	out, err := Exec(ctx, dir, args...)
	if err != nil {
		return ""
	}

	return out
}

// Refs lists the short names of the refs matching patterns, in git's own
// order. A pattern is what for-each-ref takes: a literal ref, which also
// matches everything under it at a slash, or a glob.
func Refs(ctx context.Context, dir string, patterns ...string) ([]string, error) {
	args := append([]string{"for-each-ref", "--format=%(refname:short)"}, patterns...)

	out, err := Exec(ctx, dir, args...)
	if err != nil {
		return nil, err
	}

	return strings.Fields(out), nil
}

// A Worktree is one checkout git knows about.
type Worktree struct {
	Path   string
	Branch string // short name; "" when detached or bare
}

// Worktrees lists every checkout of the repo dir belongs to, the main one
// first, as `git worktree list --porcelain` reports them.
func Worktrees(ctx context.Context, dir string) ([]Worktree, error) {
	out, err := Exec(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	return parseWorktrees(out), nil
}

// parseWorktrees reads the porcelain format: entries separated by a blank
// line, each opened by a `worktree <path>` line.
func parseWorktrees(out string) []Worktree {
	var (
		list []Worktree
		cur  *Worktree
	)

	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			cur = &list[len(list)-1]
		case cur != nil && strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}

	return list
}

// AddWorktree checks branch out at path. With create, the branch is made
// from base - git's HEAD when base is "" - and must not exist yet;
// otherwise it must.
func AddWorktree(ctx context.Context, dir, path, branch, base string, create bool) error {
	args := []string{"worktree", "add"}

	if create {
		args = append(args, "-b", branch, path)
		if base != "" {
			args = append(args, base)
		}
	} else {
		args = append(args, path, branch)
	}

	_, err := Exec(ctx, dir, args...)

	return err
}
