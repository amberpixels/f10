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

// init writes into a real repo, so these run the real command tree against
// real repos: what it creates, and the three cases where it creates nothing.

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
func runInitIn(t *testing.T, dir string, flags ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer = &out

	err := app.Run(context.Background(), append([]string{"f10", "-C", dir, "init"}, flags...))

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

// mkRepo is a checkout named demo: the name derives the task id prefix
// init writes, and a temp dir's own basename derives none.
func mkRepo(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	dir = canonical(t, dir)

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

// canonical matches the layout package, which reports physical paths - a temp dir
// on macOS is reached through a symlink.
func canonical(t *testing.T, path string) string {
	t.Helper()

	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalizing %s: %v", path, err)
	}

	return p
}

// The remote review's four facts come from the CI workflow when one runs the
// Claude Code action, and from nowhere otherwise: a checkout without one
// declares no remote review rather than a guessed one.
func TestInitWritesTheReviewFromAWorkflow(t *testing.T) {
	isolateEnv(t)

	bare := mkRepo(t)

	out, err := runInitIn(t, bare)
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

	repo := mkRepo(t)
	dir := filepath.Join(repo, ".github", "workflows")

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	workflow := "on:\n  pull_request:\n    types: [opened, synchronize]\njobs:\n  review:\n    runs-on: ubuntu-latest\n" +
		"    steps:\n      - uses: anthropics/claude-code-action@v1\n        with:\n          prompt: review\n"
	if err := os.WriteFile(filepath.Join(dir, "claude-review.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runInitIn(t, repo); err != nil {
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

func TestInitMigrate(t *testing.T) {
	isolateEnv(t)

	repo := mkRepo(t)
	path := filepath.Join(repo, ".f10", "instructions", "project.md")
	prose := "# demo\n\n## Tracker\n\nGitHub Issues. Task ids `DM-###`.\n\n" +
		"## Ship pipelines\n\n- **local**: `implement → commit`\n"

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(prose), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runInitIn(t, repo, "--migrate")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	written, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(written), "---\n") || !strings.HasSuffix(string(written), prose) {
		t.Errorf("want a block above the untouched prose:\n%s", written)
	}

	for _, want := range []string{"moved", "flows", "delete"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}

	if _, err := runInitIn(t, repo, "--migrate"); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Errorf("a second migrate = %v", err)
	}
}

// A frontmatter still saying review_traces is renamed to traces in place;
// nothing is superseded, so the report points at config instead of at prose
// to delete.
func TestInitMigrateRenamesTraces(t *testing.T) {
	isolateEnv(t)

	repo := mkRepo(t)
	path := filepath.Join(repo, ".f10", "instructions", "project.md")
	md := "---\nreview_traces: on\n---\n# demo\n"

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runInitIn(t, repo, "--migrate")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	written, _ := os.ReadFile(path)
	if string(written) != "---\ntraces: on\n---\n# demo\n" {
		t.Errorf("renamed file:\n%s", written)
	}

	if !strings.Contains(out, "traces") || strings.Contains(out, "delete") || !strings.Contains(out, "f10 config") {
		t.Errorf("report:\n%s", out)
	}
}

// A fact the two places declare differently stops the commands that act on
// facts; config still runs, since it is how the problem is seen.
func TestDeclarationErrorStopsCommands(t *testing.T) {
	isolateEnv(t)

	repo := mkRepo(t)
	path := filepath.Join(repo, ".f10", "instructions", "project.md")

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	md := "---\nmerge_method: rebase\n---\n## Hosting & PR\n\nmerge method: squash\n"
	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		var out bytes.Buffer

		app := newApp()
		app.Writer = &out
		err := app.Run(context.Background(), append([]string{"f10", "-C", repo}, args...))

		return out.String(), err
	}

	if _, err := run("task", "read", "1"); err == nil || !strings.Contains(err.Error(), "merge_method") {
		t.Errorf("task read = %v, want the declaration error", err)
	}

	out, err := run("config")
	if err != nil || !strings.Contains(out, "error: merge_method") {
		t.Errorf("config = %v:\n%s", err, out)
	}
}
