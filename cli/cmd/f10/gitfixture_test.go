package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Dates the git fixtures commit at: before a period, during it, and after.
const (
	before = "2026-10-10T10:00:00Z"
	during = "2026-10-14T10:00:00Z"
	later  = "2026-10-14T12:00:00Z"
)

// gitRepo is a real repo a test builds. TestMain already keeps the
// developer's git config out, so identity comes from the repo's own config.
type gitRepo struct {
	t   *testing.T
	dir string
}

// initRepo makes dir a repo on branch, with the default fixture identity.
func initRepo(t *testing.T, dir, branch string) gitRepo {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	r := gitRepo{t, dir}
	r.git("init", "-q", "-b", branch)
	r.identity("me@x.io", "Me")

	return r
}

// cloneRepo is a clone named name of an empty bare origin whose default
// branch is main; the caller commits and pushes the first history.
func cloneRepo(t *testing.T, name string) gitRepo {
	t.Helper()

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")

	gitRepo{t, root}.git("init", "-q", "--bare", "--initial-branch=main", origin)
	gitRepo{t, root}.git("clone", "-q", origin, filepath.Join(root, name))

	r := gitRepo{t, filepath.Join(root, name)}
	r.identity("me@x.io", "Me")

	return r
}

func (r gitRepo) identity(email, name string) {
	r.t.Helper()

	r.git("config", "user.email", email)
	r.git("config", "user.name", name)
}

func (r gitRepo) git(args ...string) {
	r.t.Helper()

	git(r.t, r.dir, args...)
}

// at runs git with a fixed author and committer date.
func (r gitRepo) at(when string, args ...string) string {
	r.t.Helper()

	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)

	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

func (r gitRepo) commit(when, file, content, msg string) {
	r.t.Helper()

	if err := os.WriteFile(filepath.Join(r.dir, file), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}

	r.at(when, "add", file)
	r.at(when, "commit", "-q", "-m", msg)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return string(out)
}
