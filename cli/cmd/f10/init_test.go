package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/testkit"
)

// init writes into a real repo, so these run the real command tree against
// real repos.

func TestInitWritesAndRefusesToOverwrite(t *testing.T) {
	testkit.IsolateEnv(t)

	repo := testkit.MkRepo(t)

	out, err := runApp(t, "-C", repo, "init").outErr()
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	path := filepath.Join(repo, ".f10", "instructions", "project.md")

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading what init wrote: %v", err)
	}

	for _, want := range []string{"visibility: stealth", "storage: in-repo", "## Tracker", "id: DEM-###"} {
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
	if status := testkit.Git(t, repo, "status", "--porcelain"); strings.Contains(status, ".f10") {
		t.Errorf("git sees .f10 after init: %q", status)
	}

	if _, err := runApp(t, "-C", repo, "init").outErr(); err == nil {
		t.Fatal("second init succeeded; it must refuse rather than overwrite")
	}

	again, _ := os.ReadFile(path)
	if !bytes.Equal(again, written) {
		t.Error("the refused run changed the file")
	}
}

func TestInitDoesNotDuplicateTheExcludeEntry(t *testing.T) {
	testkit.IsolateEnv(t)

	repo := testkit.MkRepo(t)

	path := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.WriteFile(path, []byte("*.tmp\n.f10/\n"), 0o644); err != nil {
		t.Fatalf("seeding exclude: %v", err)
	}

	if _, err := runApp(t, "-C", repo, "init").outErr(); err != nil {
		t.Fatalf("init: %v", err)
	}

	if got := strings.Count(readExclude(t, repo), ".f10/"); got != 1 {
		t.Errorf(".f10/ appears %d times in info/exclude, want 1", got)
	}
}

func TestInitRefusesInALinkedWorktree(t *testing.T) {
	testkit.IsolateEnv(t)

	repo := testkit.MkRepo(t)
	testkit.Git(t, repo, "commit", "-q", "--allow-empty", "-m", "root")

	wt := filepath.Join(t.TempDir(), "feature")
	testkit.Git(t, repo, "worktree", "add", "-q", "-b", "feature", wt)

	_, err := runApp(t, "-C", wt, "init").outErr()
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
	testkit.IsolateEnv(t)

	dir := testkit.Canonical(t, t.TempDir())

	if _, err := runApp(t, "-C", dir, "init").outErr(); err == nil {
		t.Fatal("init outside a git checkout succeeded")
	}
}

func TestInitWritesOutOfTreeWithoutTouchingTheRepo(t *testing.T) {
	home := testkit.IsolateEnv(t)
	repo := testkit.MkRepo(t)

	oot := filepath.Join(testkit.Canonical(t, home), ".f10", filepath.Base(repo), "instructions")
	if err := os.MkdirAll(oot, 0o755); err != nil {
		t.Fatalf("creating the out-of-tree root: %v", err)
	}

	if _, err := runApp(t, "-C", repo, "init").outErr(); err != nil {
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

func readExclude(t *testing.T, repo string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading info/exclude: %v", err)
	}

	return string(data)
}

// The remote review's four facts come from the CI workflow when one runs the
// Claude Code action, and from nowhere otherwise: a checkout without one
// declares no remote review rather than a guessed one.
func TestInitWritesTheReviewFromAWorkflow(t *testing.T) {
	testkit.IsolateEnv(t)

	bare := testkit.MkRepo(t)

	out, err := runApp(t, "-C", bare, "init").outErr()
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	written, _ := os.ReadFile(filepath.Join(bare, ".f10", "instructions", "project.md"))
	if strings.Contains(string(written), "## Review") {
		t.Errorf("a checkout without a review workflow declared one:\n%s", written)
	}

	if !strings.Contains(out, "Review") {
		t.Errorf("the report's left-out row does not name Review:\n%s", out)
	}

	repo := testkit.MkRepo(t)
	dir := filepath.Join(repo, ".github", "workflows")

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	workflow := "on:\n  pull_request:\n    types: [opened, synchronize]\njobs:\n  review:\n    runs-on: ubuntu-latest\n" +
		"    steps:\n      - uses: anthropics/claude-code-action@v1\n        with:\n          prompt: review\n"
	if err := os.WriteFile(filepath.Join(dir, "claude-review.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runApp(t, "-C", repo, "init").outErr(); err != nil {
		t.Fatalf("init: %v", err)
	}

	written, _ = os.ReadFile(filepath.Join(repo, ".f10", "instructions", "project.md"))

	for _, want := range []string{
		"review:",
		"reviewer: bot `claude[bot]`",
		"arrives: issue comment",
		"trigger: automatic, on open and on push",
		"done: posted, reaction `eyes`",
		"settled: reaction `rocket`",
	} {
		if !strings.Contains(string(written), want) {
			t.Errorf("project.md missing %q:\n%s", want, written)
		}
	}
}

// A value outside a key's vocabulary stops the commands that act on facts;
// config still runs, since it is how the problem is seen.
func TestDeclarationErrorStopsCommands(t *testing.T) {
	testkit.IsolateEnv(t)

	repo := testkit.MkRepo(t)
	path := filepath.Join(repo, ".f10", "instructions", "project.md")

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	md := "---\nmerge_method: fast-forward\n---\n## Hosting & PR\n\ngithub.com\n"
	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		return runApp(t, append([]string{"-C", repo}, args...)...).outErr()
	}

	if _, err := run("task", "read", "1"); err == nil || !strings.Contains(err.Error(), "merge_method") {
		t.Errorf("task read = %v, want the declaration error", err)
	}

	out, err := run("config")
	if err != nil || !strings.Contains(out, `merge_method: "fast-forward" is not one of`) {
		t.Errorf("config = %v:\n%s", err, out)
	}
}
