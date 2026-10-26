package lore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNoRecord is a valid empty answer: no entries, no trailer, a line not
// committed yet, no email configured. It is never a git failure.
var ErrNoRecord = errors.New("lore: no record")

// Git is all lore asks of git. Paths are relative to the repository root.
type Git interface {
	// BlameLine is the commit that last touched the line; ErrNoRecord when
	// the line is not committed yet.
	BlameLine(ctx context.Context, path string, line int) (string, error)
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
func (g CLIGit) BlameLine(ctx context.Context, path string, line int) (string, error) {
	n := strconv.Itoa(line)

	out, err := g.run(ctx, "blame", "--porcelain", "-L", n+","+n, "--", path)
	if err != nil {
		// a file on disk that HEAD lacks is not committed yet, unlike a typo
		var ge *gitError
		if errors.As(err, &ge) && strings.Contains(ge.stderr, "no such path") {
			if _, statErr := os.Stat(filepath.Join(g.Dir, path)); statErr == nil {
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
