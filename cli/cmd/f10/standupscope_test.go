package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// scopeRepo is a checkout with one empty commit, named name under parent.
func scopeRepo(t *testing.T, parent, name string) string {
	t.Helper()

	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "me@x.io")
	git(t, dir, "config", "user.name", "me")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "init")

	return dir
}

func standupJSON(t *testing.T, args ...string) standup.Standup {
	t.Helper()

	out, err := runStandupApp(t, append(args, "standup", "--json", "--week")...)
	if err != nil {
		t.Fatal(err)
	}

	var s standup.Standup
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	return s
}

func repoList(s standup.Standup) string {
	var names []string

	for _, p := range s.Projects {
		for _, r := range p.Repos {
			names = append(names, r.Name)
		}
	}

	return strings.Join(names, " ")
}

// Every -C is read, in order; a worktree and its main checkout are one
// repository, read once. A comma is part of a path, not a separator.
func TestStandupReadsEveryCheckoutOnce(t *testing.T) {
	isolateEnv(t)

	root := canonical(t, t.TempDir())
	a := scopeRepo(t, root, "svc,a")
	b := scopeRepo(t, root, "svc-b")
	tree := filepath.Join(root, "svc-b.wt")
	git(t, b, "worktree", "add", "-q", "-b", "side", tree)

	s := standupJSON(t, "-C", a, "-C", tree, "-C", b)

	if got, want := repoList(s), "svc,a svc-b"; got != want {
		t.Errorf("repos = %q, want %q", got, want)
	}

	if s.Schema != standup.Schema || len(s.Projects) != 2 {
		t.Errorf(
			"schema %d, %d projects: want %d and one project per repository",
			s.Schema,
			len(s.Projects),
			standup.Schema,
		)
	}
}

// Other commands answer for one checkout.
func TestOnlyStandupTakesSeveralCheckouts(t *testing.T) {
	isolateEnv(t)

	root := canonical(t, t.TempDir())
	a, b := scopeRepo(t, root, "a"), scopeRepo(t, root, "b")

	if _, err := runStandupApp(t, "-C", a, "-C", b, "config"); err == nil ||
		!strings.Contains(err.Error(), "only standup") {
		t.Errorf("err = %v, want two -C refused outside standup", err)
	}
}

// A group lists paths and globs: a glob skips what is not a checkout, a
// path that is not one is an error, and an unknown group names the
// declared ones.
func TestStandupGroups(t *testing.T) {
	home := isolateEnv(t)

	root := canonical(t, t.TempDir())
	scopeRepo(t, root, "acme-api")
	scopeRepo(t, root, "acme-web")

	if err := os.MkdirAll(filepath.Join(root, "acme-notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeUserFile(t, home, standupConfigFile,
		"---\ngroups:\n  work: ["+root+"/acme-*]\n  broken: ["+root+"/acme-notes]\n---\n")

	out, err := runStandupApp(t, "standup", "--json", "--week", "--group", "work")
	if err != nil {
		t.Fatal(err)
	}

	var s standup.Standup
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatal(err)
	}

	if got, want := repoList(s), "acme-api acme-web"; got != want {
		t.Errorf("repos = %q, want %q", got, want)
	}

	if _, err := runStandupApp(t, "standup", "--week", "--group", "broken"); err == nil ||
		!strings.Contains(err.Error(), "not a git checkout") {
		t.Errorf("err = %v, want the non-checkout path named", err)
	}

	if _, err := runStandupApp(t, "standup", "--week", "--group", "play"); err == nil ||
		!strings.Contains(err.Error(), "declared: broken, work") {
		t.Errorf("err = %v, want the declared groups listed", err)
	}
}

// --all is the F10_ROOTS walk, so it is off until the variable is set.
func TestStandupAllNeedsRoots(t *testing.T) {
	isolateEnv(t)
	t.Setenv(projects.RootsEnv, "")

	if _, err := runStandupApp(t, "standup", "--week", "--all"); err == nil ||
		!strings.Contains(err.Error(), projects.RootsEnv) {
		t.Errorf("err = %v, want %s named", err, projects.RootsEnv)
	}

	root := canonical(t, t.TempDir())
	scopeRepo(t, root, "r1")
	scopeRepo(t, root, "r2")
	t.Setenv(projects.RootsEnv, filepath.Join(root, "*"))

	out, err := runStandupApp(t, "standup", "--json", "--week", "--all")
	if err != nil {
		t.Fatal(err)
	}

	var s standup.Standup
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatal(err)
	}

	if got, want := repoList(s), "r1 r2"; got != want {
		t.Errorf("repos = %q, want %q", got, want)
	}
}
