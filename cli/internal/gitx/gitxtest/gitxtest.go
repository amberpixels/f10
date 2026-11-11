// Package gitxtest builds real git repositories for tests, isolated from
// the developer's git config so no global setting shapes a fixture.
package gitxtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolated keeps the developer's global and system config out of every call.
var isolated = []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}

// A Repo is a real repository a test builds.
type Repo struct {
	T   testing.TB
	Dir string
}

// Init makes dir a repo on branch, with the default fixture identity.
func Init(t testing.TB, dir, branch string) Repo {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	r := Repo{t, dir}
	r.Git("init", "-q", "-b", branch)
	r.Identity("me@x.io", "Me")

	return r
}

// Clone is a clone named name of an empty bare origin whose default branch
// is main; the caller commits and pushes the first history.
func Clone(t testing.TB, name string) Repo {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")

	Repo{t, root}.Git("init", "-q", "--bare", "--initial-branch=main", origin)
	Repo{t, root}.Git("clone", "-q", origin, filepath.Join(root, name))

	r := Repo{t, filepath.Join(root, name)}
	r.Identity("me@x.io", "Me")

	return r
}

func (r Repo) Identity(email, name string) {
	r.T.Helper()

	r.Git("config", "user.email", email)
	r.Git("config", "user.name", name)
}

// Git runs git in the repo and returns its output.
func (r Repo) Git(args ...string) string {
	r.T.Helper()

	return Run(r.T, r.Dir, args...)
}

// At runs git with a fixed author and committer date, its output trimmed.
func (r Repo) At(when string, args ...string) string {
	r.T.Helper()

	return strings.TrimSpace(run(r.T, r.Dir, []string{"GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when}, args))
}

// Commit writes content to file and commits it at when.
func (r Repo) Commit(when, file, content, msg string) {
	r.T.Helper()

	if err := os.WriteFile(filepath.Join(r.Dir, file), []byte(content), 0o600); err != nil {
		r.T.Fatal(err)
	}

	r.At(when, "add", file)
	r.At(when, "commit", "-q", "-m", msg)
}

// Run runs git in dir and returns its output.
func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()

	return run(t, dir, nil, args)
}

func run(t testing.TB, dir string, env, args []string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), isolated...), env...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return string(out)
}
