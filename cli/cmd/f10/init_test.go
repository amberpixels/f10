package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// init is the binary's only writing verb, so these run the real command
// tree against real repos: what it creates, and the three cases where it
// creates nothing.

func TestInitWritesAndRefusesToOverwrite(t *testing.T) {
	isolateEnv(t)

	repo := mkRepo(t)

	out, err := runInitIn(t, repo)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	path := filepath.Join(repo, ".f10", "instructions", "project.md")

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading what init wrote: %v", err)
	}

	for _, want := range []string{"## Visibility", "## Storage", "## Tracker", "GH-###"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("project.md missing %q:\n%s", want, written)
		}
	}

	for _, want := range []string{"wrote", path, "next"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}

	if excl := readExclude(t, repo); !strings.Contains(excl, ".f10/\n") {
		t.Errorf("exclude entry not written: %q", excl)
	}

	// git must not see the directory init just created
	if status := git(t, repo, "status", "--porcelain"); strings.Contains(status, ".f10") {
		t.Errorf("git sees .f10 after init: %q", status)
	}

	if _, err := runInitIn(t, repo); err == nil {
		t.Fatal("second init succeeded; it must refuse rather than overwrite")
	}

	again, _ := os.ReadFile(path)
	if !bytes.Equal(again, written) {
		t.Error("the refused run changed the file")
	}
}

func TestInitDoesNotDuplicateTheExcludeEntry(t *testing.T) {
	isolateEnv(t)

	repo := mkRepo(t)

	path := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.WriteFile(path, []byte("*.tmp\n.f10/\n"), 0o644); err != nil {
		t.Fatalf("seeding exclude: %v", err)
	}

	if _, err := runInitIn(t, repo); err != nil {
		t.Fatalf("init: %v", err)
	}

	if got := strings.Count(readExclude(t, repo), ".f10/"); got != 1 {
		t.Errorf(".f10/ appears %d times in info/exclude, want 1", got)
	}
}

func TestInitRefusesInALinkedWorktree(t *testing.T) {
	isolateEnv(t)

	repo := mkRepo(t)
	git(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "root")

	wt := filepath.Join(t.TempDir(), "feature")
	git(t, repo, "worktree", "add", "-q", "-b", "feature", wt)

	_, err := runInitIn(t, wt)
	if err == nil {
		t.Fatal("init in a linked worktree succeeded")
	}

	if !strings.Contains(err.Error(), repo) {
		t.Errorf("the refusal does not name the main checkout: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wt, ".f10")); !os.IsNotExist(err) {
		t.Error("init wrote into the worktree it refused")
	}
}

func TestInitRefusesOutsideGit(t *testing.T) {
	isolateEnv(t)

	dir := canonical(t, t.TempDir())

	if _, err := runInitIn(t, dir); err == nil {
		t.Fatal("init outside a git checkout succeeded")
	}
}

func TestInitWritesOutOfTreeWithoutTouchingTheRepo(t *testing.T) {
	home := isolateEnv(t)
	repo := mkRepo(t)

	oot := filepath.Join(canonical(t, home), ".f10", filepath.Base(repo), "instructions")
	if err := os.MkdirAll(oot, 0o755); err != nil {
		t.Fatalf("creating the out-of-tree root: %v", err)
	}

	if _, err := runInitIn(t, repo); err != nil {
		t.Fatalf("init: %v", err)
	}

	if _, err := os.Stat(filepath.Join(oot, "project.md")); err != nil {
		t.Fatalf("nothing written out of tree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, ".f10")); !os.IsNotExist(err) {
		t.Error("out-of-tree storage left a .f10 inside the project")
	}

	if strings.Contains(readExclude(t, repo), ".f10/") {
		t.Error("out-of-tree storage wrote an exclude entry it does not need")
	}
}

// runInitIn runs `f10 -C <dir> init` through the real command tree and
// returns what the command printed.
func runInitIn(t *testing.T, dir string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer = &out

	err := app.Run(context.Background(), []string{"f10", "-C", dir, "init"})

	return out.String(), err
}

// isolateEnv keeps the probes off the developer's own machine: both the
// gh/glab config lookup and the out-of-tree storage root hang off HOME.
func isolateEnv(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	return home
}

func mkRepo(t *testing.T) string {
	t.Helper()

	dir := canonical(t, t.TempDir())

	git(t, dir, "init", "-q")
	git(t, dir, "remote", "add", "origin", "git@github.com:amberpixels/demo.git")

	return dir
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

func readExclude(t *testing.T, repo string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading info/exclude: %v", err)
	}

	return string(data)
}

// canonical matches the resolver, which reports physical paths - a temp dir
// on macOS is reached through a symlink.
func canonical(t *testing.T, path string) string {
	t.Helper()

	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalizing %s: %v", path, err)
	}

	return p
}
