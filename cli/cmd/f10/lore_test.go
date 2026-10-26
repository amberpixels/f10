package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/verdict"
)

// lore keeps its own copy of the judge's outcomes so it imports nothing
// from f10; this is what keeps the copy honest.
func TestLoreOutcomesAreVerdicts(t *testing.T) {
	if !slices.Equal(lore.Outcomes, verdict.Verdicts) {
		t.Errorf("lore.Outcomes %q, verdict.Verdicts %q", lore.Outcomes, verdict.Verdicts)
	}
}

func runLore(t *testing.T, args ...string) (string, error) {
	t.Helper()

	return runLoreAt(t, append([]string{"--root", ".lore"}, args...)...)
}

// runLoreAt runs the lore noun with args as given, no --root added.
func runLoreAt(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &out

	err := app.Run(t.Context(), append([]string{"f10", "lore"}, args...))

	return out.String(), err
}

func TestLoreVerbs(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("F10_AGENT_MODEL", "")
	t.Setenv("F10_AGENT_SESSION", "")

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-q")
	git("config", "user.email", "dev@example.com")
	git("config", "user.name", "Dev")

	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "api", "limit.go"), []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	git("add", ".")
	git("commit", "-q", "-m", "Add the limiter\n\nTask: FT-1")

	// paths are given relative to the cwd, a subdirectory here
	t.Chdir(filepath.Join(dir, "api"))

	must := func(args ...string) string {
		t.Helper()

		out, err := runLore(t, args...)
		if err != nil {
			t.Fatalf("lore %q: %v\n%s", args, err, out)
		}

		return strings.TrimSpace(out)
	}

	must("add", "intent", "--change", "FT-1", "-m", "Limit the API")
	must(
		"add",
		"decision",
		"--change",
		"FT-1",
		"--anchor",
		"limit.go:2-3#Allow",
		"--chosen",
		"token bucket",
		"--rejected",
		"fixed window: edge bursts",
		"--agent-model",
		"claude-opus-5-5",
		"-m",
		"Per key",
	)
	open := must("add", "finding", "--change", "FT-1", "--anchor", "limit.go:3",
		"--severity", "major", "--status", "open", "-m", "Never pruned")

	if _, err := runLore(t, "check", "FT-1"); err == nil || !strings.Contains(err.Error(), "no verdict") ||
		!strings.Contains(err.Error(), "major finding "+open) {
		t.Errorf("check before the verdict: %v", err)
	}

	must("add", "finding", "--change", "FT-1", "--anchor", "limit.go:3", "--supersedes", open,
		"--severity", "major", "--status", "fixed", "--resolution", "abcdef1", "-m", "Pruned")
	must("add", "verdict", "--change", "FT-1", "--outcome", "proceed with changes", "--gate", "judge",
		"--commit", "abcdef1", "-m", "Fine")
	must("add", "lesson", "--change", "FT-1", "--anchor", "limit.go:2", "-m", "Per process")

	if out := must("check", "FT-1"); out != "FT-1 passes" {
		t.Errorf("check = %q", out)
	}

	show := must("show", "FT-1")
	if !strings.HasPrefix(show, "FT-1  5 entries") || !strings.Contains(show, "anchor: api/limit.go:2-3@") ||
		!strings.Contains(show, "with claude-opus-5-5") || strings.Contains(show, "Never pruned") {
		t.Errorf("show:\n%s", show)
	}

	why := must("why", "limit.go:3")
	for _, want := range []string{"api/limit.go:3", "linked by the commit's trailer", "Limit the API", "proceed with changes", "Per key", "Pruned"} {
		if !strings.Contains(why, want) {
			t.Errorf("why lacks %q:\n%s", want, why)
		}
	}

	if out := must("why", "limit.go:1"); !strings.Contains(out, "== FT-1") {
		t.Errorf("why off the anchors still names the change:\n%s", out)
	}

	if out := must("lessons", "limit.go"); !strings.Contains(out, "1 lesson") || !strings.Contains(out, "Per process") {
		t.Errorf("lessons:\n%s", out)
	}

	if out := must("lessons", "--json", "../README.md"); out != "[]" {
		t.Errorf("lessons --json for a file with none = %q", out)
	}

	if out := must("validate"); out != "1 change valid" {
		t.Errorf("validate = %q", out)
	}

	if _, err := runLore(t, "add", "note", "--change", "FT-1", "-m", "x"); err == nil {
		t.Error("an unknown kind was written")
	}

	if _, err := runLore(t, "why", "../../outside.go:1"); err == nil {
		t.Error("why answered for a path outside the repository")
	}
}

func TestLoreWhyNoRecord(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	dir := t.TempDir()

	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=a@b.c", "-c", "user.name=A", "commit", "-q", "--allow-empty", "-m", "empty"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	out, err := runLore(t, "why", "new.go:1")
	if err != nil || strings.TrimSpace(out) != "no record" {
		t.Errorf("why on an uncommitted line = %q, %v", out, err)
	}
}

// An entry added from a task's worktree lands in main's storage root, so
// removing the worktree when the task finishes keeps it.
func TestLoreLocalRootIsMains(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	main := t.TempDir()
	wt := filepath.Join(t.TempDir(), "wt")

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "dev@example.com"},
		{"config", "user.name", "Dev"},
		{"commit", "-q", "--allow-empty", "-m", "root"},
		{"worktree", "add", "-q", "-b", "FT-1/x", wt},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = main

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	t.Chdir(wt)

	out, err := runLoreAt(t, "add", "intent", "--change", "FT-1", "-m", "why")
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}

	mainRoot, _ := filepath.EvalSymlinks(main)
	id := strings.TrimSpace(out)

	if _, err := os.Stat(filepath.Join(mainRoot, ".f10", "lore", "FT-1", id+"-intent.md")); err != nil {
		t.Errorf("entry not in main's storage root: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wt, ".f10", "lore")); err == nil {
		t.Error("the worktree holds a lore root of its own")
	}
}

func TestLoreAddAnchorBeforeFirstCommit(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	dir := t.TempDir()

	cmd := exec.CommandContext(t.Context(), "git", "init", "-q")
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	t.Chdir(dir)

	_, err := runLore(t, "add", "lesson", "--change", "FT-1", "--anchor", "a.go:1", "-m", "x")
	if err == nil || !strings.Contains(err.Error(), "no commits yet") {
		t.Errorf("err = %v, want one saying the repository has no commits", err)
	}
}
