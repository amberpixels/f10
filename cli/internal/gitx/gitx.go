// Package gitx is the binary's one door to git. The reads mirror the
// shell-outs bin/f10-load-project.sh makes - the bash script is the semantics
// oracle, and shelling out identically keeps the two comparable. The
// writes are `f10 start`'s - a branch (new, or tracking origin's), a
// worktree, and the config entries that record what a branch depends on - and `f10 finish`'s, which undoes
// them once the branch has merged: the pull, the worktree removal, the
// branch deletion. `f10 drive` adds one: the push of a branch it caught up
// before merging.
//
// Everything goes through Exec, a variable so a test can answer git
// without a checkout. No go-git.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Exec runs git with args in dir and returns trimmed stdout. A non-zero
// exit is an *ExitError carrying git's own stderr, which names the cause
// better than any wrapper could, and the code, which Read needs.
var Exec = func(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && ctx.Err() == nil {
			return "", &ExitError{Args: args, Code: exit.ExitCode(), Stderr: strings.TrimSpace(stderr.String())}
		}

		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}

		return "", fmt.Errorf("git %s: %w", args[0], err)
	}

	return strings.TrimSpace(string(out)), nil
}

// An ExitError is git having run and exited non-zero.
type ExitError struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("git %s: %s", e.Args[0], e.Stderr)
	}

	return fmt.Sprintf("git %s: exit status %d", e.Args[0], e.Code)
}

// absent reports whether err is git's exit 1, which the quiet forms of a
// read (`config --get`, `rev-parse -q --verify`, `symbolic-ref -q`,
// `merge-base --is-ancestor`) use for "not there" or "no".
func absent(err error) bool {
	exit, ok := errors.AsType[*ExitError](err)

	return ok && exit.Code == 1
}

// Out runs git and returns trimmed stdout, or "" on any error. It cannot
// tell "not there" from "git failed", so it is for display only: a read
// that decides what f10 does next goes through Read.
func Out(ctx context.Context, dir string, args ...string) string {
	out, err := Exec(ctx, dir, args...)
	if err != nil {
		return ""
	}

	return out
}

// Read runs a quiet git read: exit 0 is the value and present, exit 1 is
// absent with no error, and anything else - a lock, a broken worktree, a
// killed git - is the error. A form whose "not there" exits 128 (a
// rev-parse --verify without -q) reads absence as an error, which halts
// rather than proceeds.
func Read(ctx context.Context, dir string, args ...string) (string, bool, error) {
	out, err := Exec(ctx, dir, args...)

	switch {
	case err == nil:
		return out, true, nil
	case absent(err):
		return "", false, nil
	default:
		return "", false, err
	}
}

// Config is one local config value; absent is "" with no error.
func Config(ctx context.Context, dir, key string) (string, error) {
	v, _, err := Read(ctx, dir, "config", "--get", key)

	return v, err
}

// Ref is the sha ref names, "" with no error when there is no such ref.
func Ref(ctx context.Context, dir, ref string) (string, error) {
	sha, _, err := Read(ctx, dir, "rev-parse", "-q", "--verify", ref)

	return sha, err
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

// Fetch updates every remote-tracking branch from origin.
func Fetch(ctx context.Context, dir string) error {
	_, err := Exec(ctx, dir, "fetch", "origin")

	return err
}

// TrackBranch creates local branch from origin's branch of the same name,
// tracking it, so a pull or push in its worktree needs no upstream set.
// With force an existing branch is moved there, which git refuses while a
// worktree has it checked out.
func TrackBranch(ctx context.Context, dir, branch string, force bool) error {
	args := []string{"branch", "--track", branch, "origin/" + branch}
	if force {
		args = []string{"branch", "--force", "--track", branch, "origin/" + branch}
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

// IsAncestor reports whether commit is reachable from ref: git answers
// with exit 0 or 1. An unknown object is a no too, since a recorded sha may
// name a commit squashed away; anything else is an error.
func IsAncestor(ctx context.Context, dir, commit, ref string) (bool, error) {
	_, err := Exec(ctx, dir, "merge-base", "--is-ancestor", commit, ref)

	switch {
	case err == nil:
		return true, nil
	case absent(err) || unknownObject(err):
		return false, nil
	default:
		return false, err
	}
}

// quiet reports a git exit with nothing on stderr.
func quiet(err error) bool {
	exit, ok := errors.AsType[*ExitError](err)

	return ok && exit.Stderr == ""
}

func unknownObject(err error) bool {
	exit, ok := errors.AsType[*ExitError](err)

	return ok && (strings.Contains(exit.Stderr, "Not a valid") || strings.Contains(exit.Stderr, "bad object"))
}

// MergesCleanly reports whether head merges into base without a conflict,
// tried in memory the way a host tries a pull request: nothing is checked
// out or written. git exits 1 both for a conflict, reported on stdout, and
// for a ref it cannot merge, reported on stderr, which is an error.
// Not --quiet: git 2.56 exits 0 with it on a conflict.
func MergesCleanly(ctx context.Context, dir, base, head string) (bool, error) {
	_, err := Exec(ctx, dir, "merge-tree", "--write-tree", base, head)

	switch {
	case err == nil:
		return true, nil
	case absent(err) && quiet(err):
		return false, nil
	default:
		return false, err
	}
}

// MergeTree is the tree of head merged into base, built in memory as
// MergesCleanly tries it; a conflict is an error.
func MergeTree(ctx context.Context, dir, base, head string) (string, error) {
	out, err := Exec(ctx, dir, "merge-tree", "--write-tree", base, head)
	if err != nil {
		return "", err
	}

	tree, _, _ := strings.Cut(out, "\n")

	return tree, nil
}

// CommitTree writes a commit of tree on parents with a message of one
// paragraph per part, and returns its sha. No ref moves.
func CommitTree(ctx context.Context, dir, tree string, parents []string, message ...string) (string, error) {
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}

	for _, m := range message {
		args = append(args, "-m", m)
	}

	return Exec(ctx, dir, args...)
}

// UpdateRef moves ref to sha while it still points at old.
func UpdateRef(ctx context.Context, dir, ref, sha, old string) error {
	_, err := Exec(ctx, dir, "update-ref", ref, sha, old)

	return err
}

// Push sends HEAD to origin's branch. A lease (the sha origin's branch had
// when the caller read it) forces the push only while origin still has that
// sha, which a rebased branch needs; "" pushes a fast-forward and nothing
// else.
func Push(ctx context.Context, dir, branch, lease string) error {
	args := []string{"push", "origin", "HEAD:refs/heads/" + branch}
	if lease != "" {
		args = []string{
			"push",
			"--force-with-lease=refs/heads/" + branch + ":" + lease,
			"origin",
			"HEAD:refs/heads/" + branch,
		}
	}

	_, err := Exec(ctx, dir, args...)

	return err
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

// Switch checks out branch in dir. A dirty tree can make git refuse, which
// callers rule out first.
func Switch(ctx context.Context, dir, branch string) error {
	_, err := Exec(ctx, dir, "switch", branch)

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

// DeleteRemoteRef drops the local copy of origin's branch, which a branch
// deleted through the host's API leaves behind until a pruning fetch. git
// deletes a ref that is not there without complaint.
func DeleteRemoteRef(ctx context.Context, dir, branch string) error {
	_, err := Exec(ctx, dir, "update-ref", "-d", "refs/remotes/origin/"+branch)

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

// RemoteBranches is every branch origin has, name to tip sha, in one
// network read. A checkout with no origin has none.
func RemoteBranches(ctx context.Context, dir string) (map[string]string, error) {
	if Out(ctx, dir, "remote", "get-url", "origin") == "" {
		return map[string]string{}, nil
	}

	out, err := Exec(ctx, dir, "ls-remote", "--heads", "origin")
	if err != nil {
		return nil, err
	}

	branches := map[string]string{}

	for line := range strings.Lines(out) {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok {
			branches[strings.TrimPrefix(name, "refs/heads/")] = sha
		}
	}

	return branches, nil
}

// A ConfigEntry is one key and its value as `config --get-regexp` lists them.
type ConfigEntry struct {
	Key   string
	Value string
}

// ConfigEntries lists the local config entries whose key matches pattern,
// in git's order. No match is an empty list, not an error: git exits 1 for
// it.
func ConfigEntries(ctx context.Context, dir, pattern string) ([]ConfigEntry, error) {
	out, _, err := Read(ctx, dir, "config", "--get-regexp", pattern)
	if err != nil || out == "" {
		return nil, err
	}

	var entries []ConfigEntry

	for line := range strings.SplitSeq(out, "\n") {
		key, value, _ := strings.Cut(line, " ")
		entries = append(entries, ConfigEntry{Key: key, Value: value})
	}

	return entries, nil
}

// UnsetConfig removes one entry from the repository's local config.
func UnsetConfig(ctx context.Context, dir, key string) error {
	_, err := Exec(ctx, dir, "config", "--unset", key)

	return err
}

// SamePath compares two worktree paths with symlinks resolved where they exist.
func SamePath(a, b string) bool {
	return Resolved(a) == Resolved(b)
}

// Resolved is the path with symlinks resolved. A component that does not
// exist - a removed or never-created path - keeps its name below the
// longest prefix that does, so two spellings of one place still compare.
func Resolved(p string) string {
	p = filepath.Clean(p)

	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}

	parent := filepath.Dir(p)
	if parent == p {
		return p
	}

	return filepath.Join(Resolved(parent), filepath.Base(p))
}
