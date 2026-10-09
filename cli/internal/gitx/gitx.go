// Package gitx is the binary's one door to git. The reads mirror the
// shell-outs bin/bundle.sh makes - the bash script is the semantics
// oracle, and shelling out identically keeps the two comparable. The
// writes are `f10 start`'s - a branch, a worktree, and the config entries
// that record what a branch depends on - and `f10 finish`'s, which undoes
// them once the branch has merged: the pull, the worktree removal, the
// branch deletion.
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
// could.
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

// Refs lists the refs matching patterns, in git's own order, as branch
// names: `refs/heads/` and `refs/remotes/` stripped, so a remote branch
// reads `origin/x`. The full refname is asked for rather than the short
// one, which git disambiguates to `heads/x` when a tag shares the name.
// A pattern is what for-each-ref takes: a literal ref, which also matches
// everything under it at a slash, or a glob.
func Refs(ctx context.Context, dir string, patterns ...string) ([]string, error) {
	args := append([]string{"for-each-ref", "--format=%(refname)"}, patterns...)

	out, err := Exec(ctx, dir, args...)
	if err != nil {
		return nil, err
	}

	refs := strings.Fields(out)
	for i, r := range refs {
		refs[i] = strings.TrimPrefix(strings.TrimPrefix(r, "refs/heads/"), "refs/remotes/")
	}

	return refs, nil
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

// SetConfig writes one entry to the repository's local config, replacing
// any value the key held. Local config never leaves the machine, which is
// what makes it the place for a per-branch fact in stealth mode.
func SetConfig(ctx context.Context, dir, key, value string) error {
	_, err := Exec(ctx, dir, "config", key, value)

	return err
}

// Status lists what `status --porcelain` reports for dir: one line per
// changed or untracked path, empty for a clean tree. Ignored files are
// not listed, so an excluded .f10/ never counts.
func Status(ctx context.Context, dir string) ([]string, error) {
	out, err := Exec(ctx, dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}

	if out == "" {
		return nil, nil
	}

	return strings.Split(out, "\n"), nil
}

// CurrentBranch is the branch dir has checked out, or "" when detached.
func CurrentBranch(ctx context.Context, dir string) string {
	return Out(ctx, dir, "branch", "--show-current")
}

// IsAncestor reports whether commit is reachable from ref. git answers
// with its exit code, so any failure reads as "no".
func IsAncestor(ctx context.Context, dir, commit, ref string) bool {
	_, err := Exec(ctx, dir, "merge-base", "--is-ancestor", commit, ref)

	return err == nil
}

// PullFF fast-forwards the checked-out branch from its upstream, and fails
// rather than merge when it cannot.
func PullFF(ctx context.Context, dir string) error {
	_, err := Exec(ctx, dir, "pull", "--ff-only")

	return err
}

// FetchInto updates local branch from origin's branch of the same name
// without touching the checkout: git fast-forwards a branch that is not
// checked out and refuses anything else.
func FetchInto(ctx context.Context, dir, branch string) error {
	_, err := Exec(ctx, dir, "fetch", "origin", branch+":"+branch)

	return err
}

// RemoveWorktree removes the checkout at path. A dirty tree makes git
// refuse, which callers rule out first.
func RemoveWorktree(ctx context.Context, dir, path string) error {
	_, err := Exec(ctx, dir, "worktree", "remove", path)

	return err
}

// DeleteBranch deletes the local branch whether or not git sees it as
// merged: a squash or rebase merge leaves no trace git recognises, so the
// caller's proof is the host's, not git's. The branch's config section
// goes with it.
func DeleteBranch(ctx context.Context, dir, branch string) error {
	_, err := Exec(ctx, dir, "branch", "-D", branch)

	return err
}

// RemoteBranchExists asks origin whether it still has branch, which is the
// one git read here that reaches the network.
func RemoteBranchExists(ctx context.Context, dir, branch string) (bool, error) {
	out, err := Exec(ctx, dir, "ls-remote", "--heads", "origin", branch)
	if err != nil {
		return false, err
	}

	return out != "", nil
}

// A ConfigEntry is one key and its value as `config --get-regexp` lists them.
type ConfigEntry struct {
	Key   string
	Value string
}

// ConfigEntries lists the local config entries whose key matches pattern,
// in git's order. No match is an empty list, not an error: git exits 1 for
// it, and absence is a valid answer here.
func ConfigEntries(ctx context.Context, dir, pattern string) []ConfigEntry {
	out := Out(ctx, dir, "config", "--get-regexp", pattern)
	if out == "" {
		return nil
	}

	var entries []ConfigEntry

	for line := range strings.SplitSeq(out, "\n") {
		key, value, _ := strings.Cut(line, " ")
		entries = append(entries, ConfigEntry{Key: key, Value: value})
	}

	return entries
}

// UnsetConfig removes one entry from the repository's local config.
func UnsetConfig(ctx context.Context, dir, key string) error {
	_, err := Exec(ctx, dir, "config", "--unset", key)

	return err
}
