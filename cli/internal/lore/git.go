package lore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ErrNoRecord is a valid empty answer: no entries, no trailer, a line not
// committed yet, no email configured. It is never a git failure.
var ErrNoRecord = errors.New("lore: no record")

// Git is all lore asks of git. Paths are relative to the repository root.
type Git interface {
	// BlameLine is the commit that last touched the line, in the working
	// tree or, with rev, in the file as that revision has it; ErrNoRecord
	// when the line is not committed yet, or the path is absent at rev.
	BlameLine(ctx context.Context, path string, line int, rev string) (string, error)
	// Trailers is the commit message's trailers, keys as written except
	// Change and Task, which are normalised so a lookup ignores case.
	Trailers(ctx context.Context, commit string) (map[string][]string, error)
	// UserEmail is `git config user.email`; ErrNoRecord when unset.
	UserEmail(ctx context.Context) (string, error)
	// Head is HEAD's abbreviated sha; ErrNoRecord before the first commit.
	Head(ctx context.Context) (string, error)
	// IsAncestor reports whether ancestor is reachable from commit; a sha
	// this clone does not have is false, not an error.
	IsAncestor(ctx context.Context, ancestor, commit string) (bool, error)
	// Hunks is what changed in path from commit to HEAD; ErrNoRecord when
	// HEAD has no such path, ErrNoCommit when this clone lacks the commit.
	Hunks(ctx context.Context, commit, path string) ([]Hunk, error)
}

// ErrNoCommit is an anchor's commit this clone does not have, as a branch
// commit squashed away and then pruned.
var ErrNoCommit = errors.New("lore: commit not in this clone")

// Hunk is one `@@ -old,count +new,count @@` header of a zero-context diff.
// A count of 0 on the old side is an insertion after OldLine.
type Hunk struct {
	OldLine, OldCount int
	NewLine, NewCount int
}

// CLIGit answers through the git binary, run in Dir, the repository root.
type CLIGit struct {
	Dir string
}

var _ Git = CLIGit{}

// gitError is a git run that failed: its exit code and its own stderr.
type gitError struct {
	args   []string
	code   int
	stderr string
	err    error
}

func (e *gitError) Error() string {
	if e.stderr != "" {
		return fmt.Sprintf("git %s: %s", e.args[0], e.stderr)
	}

	return fmt.Sprintf("git %s: %v", e.args[0], e.err)
}

func (e *gitError) Unwrap() error { return e.err }

// BlameLine asks blame for one line; git names an uncommitted line with an
// all-zero sha.
func (g CLIGit) BlameLine(ctx context.Context, path string, line int, rev string) (string, error) {
	n := strconv.Itoa(line)

	args := []string{"blame", "--porcelain", "-L", n + "," + n}
	if rev != "" {
		args = append(args, rev)
	}

	out, err := g.run(ctx, append(args, "--", path)...)
	if err != nil {
		// a file on disk that HEAD lacks is not committed yet, unlike a typo;
		// at a revision, a path it lacks has no line to ask about
		var ge *gitError
		if errors.As(err, &ge) && strings.Contains(ge.stderr, "no such path") {
			if _, statErr := os.Stat(filepath.Join(g.Dir, path)); rev != "" || statErr == nil {
				return "", ErrNoRecord
			}
		}

		return "", err
	}

	sha, _, _ := strings.Cut(out, " ")
	if strings.Trim(sha, "0") == "" {
		return "", ErrNoRecord
	}

	return sha, nil
}

// Trailers parses `%(trailers:only,unfold)`: one `Key: value` per line.
func (g CLIGit) Trailers(ctx context.Context, commit string) (map[string][]string, error) {
	out, err := g.run(ctx, "show", "-s", "--format=%(trailers:only,unfold)", commit, "--")
	if err != nil {
		return nil, err
	}

	trailers := map[string][]string{}

	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}

		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if value == "" {
			continue
		}

		switch strings.ToLower(key) {
		case "change":
			key = TrailerChange
		case "task":
			key = TrailerTask
		}

		trailers[key] = append(trailers[key], value)
	}

	return trailers, nil
}

// UserEmail reads the config; git exits 1 with no output for an unset key.
func (g CLIGit) UserEmail(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "config", "user.email")
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && ge.code == 1 && ge.stderr == "" {
			return "", ErrNoRecord
		}

		return "", err
	}

	email := strings.TrimSpace(out)
	if email == "" {
		return "", ErrNoRecord
	}

	return email, nil
}

// Head asks rev-parse, which fails on an unborn branch.
func (g CLIGit) Head(ctx context.Context) (string, error) {
	if _, err := g.run(ctx, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		if ge, ok := errors.AsType[*gitError](err); ok && ge.code == 1 && ge.stderr == "" {
			return "", ErrNoRecord
		}

		return "", err
	}

	out, err := g.run(ctx, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// IsAncestor reads merge-base's exit code: 0 yes, 1 no; an unknown object
// is a no too, since an anchor may name a commit squashed away.
func (g CLIGit) IsAncestor(ctx context.Context, ancestor, commit string) (bool, error) {
	_, err := g.run(ctx, "merge-base", "--is-ancestor", ancestor, commit)
	if err == nil {
		return true, nil
	}

	ge, ok := errors.AsType[*gitError](err)
	if ok && (ge.code == 1 || strings.Contains(ge.stderr, "Not a valid") || strings.Contains(ge.stderr, "bad object")) {
		return false, nil
	}

	return false, err
}

var (
	shaRe      = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)
	hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
)

// Hunks reads `git diff -U0 <commit> HEAD`; the commit comes from an entry
// file, so only a sha reaches git's argument list.
func (g CLIGit) Hunks(ctx context.Context, commit, path string) ([]Hunk, error) {
	if !shaRe.MatchString(commit) {
		return nil, ErrNoCommit
	}

	out, err := g.run(ctx, "diff", "--unified=0", "--no-color", "--no-ext-diff", "--no-renames",
		commit, "HEAD", "--", path)
	if err != nil {
		if _, catErr := g.run(ctx, "cat-file", "-e", commit+"^{commit}"); catErr != nil {
			return nil, ErrNoCommit
		}

		return nil, err
	}

	var hunks []Hunk

	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "deleted file mode") {
			return nil, ErrNoRecord
		}

		m := hunkHeader.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		h := Hunk{OldCount: hunkCount(m[2]), NewCount: hunkCount(m[4])}
		h.OldLine, _ = strconv.Atoi(m[1])
		h.NewLine, _ = strconv.Atoi(m[3])
		hunks = append(hunks, h)
	}

	return hunks, nil
}

// hunkCount reads a hunk header's count, which git leaves out when it is 1.
func hunkCount(s string) int {
	if s == "" {
		return 1
	}

	n, _ := strconv.Atoi(s)

	return n
}

func (g CLIGit) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir

	var stderr strings.Builder

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		ge := &gitError{args: args, code: -1, stderr: strings.TrimSpace(stderr.String()), err: err}

		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			ge.code = exit.ExitCode()
		}

		return "", ge
	}

	return string(out), nil
}

// The trailers that link a commit to its change; Task is f10's alias.
const (
	TrailerChange = "Change"
	TrailerTask   = "Task"
)
