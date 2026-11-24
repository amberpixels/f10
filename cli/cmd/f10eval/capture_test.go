package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

// capturedRepo is a prepared fixture whose tracker holds EV-9, then the
// tasks a run would add after the initial commit, as name to body.
func capturedRepo(t *testing.T, added map[string]string) string {
	t.Helper()

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "tasks"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(src, "tasks", "EV-9.md"), []byte("# EV-9: fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "repo")
	if err := prepare(t.Context(), src, dir); err != nil {
		t.Fatal(err)
	}

	for name, body := range added {
		if err := os.WriteFile(filepath.Join(dir, "tasks", name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func task(id, parent, after string) string {
	body := "# " + id + ": t\n\nOPEN · created 2026-11-24\n"
	if parent != "" {
		body += "Parent: " + parent + "\n"
	}

	body += "\nThe body.\n"
	if after != "" {
		body += "\nAfter: [" + after + ": base](file:///x)\n"
	}

	return body
}

func TestCapturedCheck(t *testing.T) {
	t.Parallel()

	split := map[string]string{
		"EV-10": task("EV-10", "", ""),
		"EV-11": task("EV-11", "EV-10", ""),
		"EV-12": task("EV-12", "EV-10", ""),
		"EV-13": task("EV-13", "EV-10", "EV-11"),
		"EV-14": task("EV-14", "EV-10", "EV-12"),
	}
	splitShape := &Captured{Tasks: 5, Parents: 1, Subtasks: 4, After: 2}

	for name, c := range map[string]struct {
		added  map[string]string
		want   *Captured
		pass   bool
		detail string
	}{
		"one flat task":                     {map[string]string{"EV-10": task("EV-10", "", "")}, &Captured{Tasks: 1}, true, "found 1 tasks"},
		"a three-level split":               {split, splitShape, true, "found 5 tasks, 1 parents, 4 subtasks, 2 After:"},
		"a split where one task was wanted": {split, &Captured{Tasks: 1}, false, "want 1 tasks"},
		"nothing captured":                  {nil, &Captured{Tasks: 1}, false, "found 0 tasks"},
		"a dependent under another parent": {map[string]string{
			"EV-10": task("EV-10", "", ""),
			"EV-11": task("EV-11", "EV-10", ""),
			"EV-12": task("EV-12", "", ""),
			"EV-13": task("EV-13", "EV-12", "EV-11"),
		}, &Captured{Tasks: 4, Parents: 2, Subtasks: 2, After: 1}, false, "EV-13: After: EV-11 sits under another parent"},
		"a chain four deep": {map[string]string{
			"EV-10": task("EV-10", "", ""),
			"EV-11": task("EV-11", "EV-10", ""),
			"EV-12": task("EV-12", "EV-10", "EV-11"),
			"EV-13": task("EV-13", "EV-10", "EV-12"),
		}, &Captured{Tasks: 4, Parents: 1, Subtasks: 3, After: 2}, false, "EV-13: After: EV-12, which waits on EV-11"},
		"a parent link to a task the run did not make": {map[string]string{
			"EV-10": task("EV-10", "EV-9", ""),
		}, &Captured{Tasks: 1, Parents: 1, Subtasks: 1}, false, "EV-10: parent EV-9 is not a task the run made"},
	} {
		dir := capturedRepo(t, c.added)

		got := checks["captured"](t.Context(), dir, Scenario{Captured: c.want})
		if got.Pass != c.pass || !strings.Contains(got.Detail, c.detail) {
			t.Errorf("%s = %+v, want pass %v with %q", name, got, c.pass, c.detail)
		}
	}
}

// A task the run committed still counts as captured: only the initial
// commit's tasks are the fixture's.
func TestCapturedCountsCommittedTasks(t *testing.T) {
	t.Parallel()

	dir := capturedRepo(t, map[string]string{"EV-10": task("EV-10", "", "")})

	for _, args := range [][]string{{"add", "tasks"}, {"commit", "-qm", "ship"}} {
		if out, err := git(t.Context(), dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	if c := checks["captured"](t.Context(), dir, Scenario{Captured: &Captured{Tasks: 1}}); !c.Pass {
		t.Errorf("a committed task was not counted: %+v", c)
	}
}

func TestWantVerdict(t *testing.T) {
	t.Parallel()

	dir := canned(t)

	if c := checkVerdict(t.Context(), dir, Scenario{Task: "T-1", WantVerdict: "proceed with changes"}); !c.Pass {
		t.Errorf("the expected verdict failed: %+v", c)
	}

	c := checkVerdict(t.Context(), dir, Scenario{Task: "T-1", WantVerdict: "stop"})
	if c.Pass || !strings.Contains(c.Detail, `verdict "proceed with changes", want "stop"`) {
		t.Errorf("a wrong verdict passed: %+v", c)
	}

	// the newest is file 10, not file 9, which a lexical order would pick
	verdicts := filepath.Join(dir, ".f10", "verdicts", "T-1")
	for n, v := range map[string]string{"9": "stop", "10": "proceed"} {
		if err := os.WriteFile(filepath.Join(verdicts, n+".md"), []byte("verdict: "+v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if c := checkVerdict(t.Context(), dir, Scenario{Task: "T-1", WantVerdict: "proceed"}); !c.Pass {
		t.Errorf("the newest verdict was not file 10: %+v", c)
	}
}

func TestLoadScenariosRefusesContradictions(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		body string
		err  string
	}{
		"prompts":              {`{"prompts":["a","b"]}`, ""},
		"prompt and prompts":   {`{"prompt":"a","prompts":["b"]}`, "both prompt and prompts"},
		"branch and change":    {`{"branch":"b","change":"c"}`, "both branch and change"},
		"captured, no check":   {`{"captured":{"tasks":1}}`, "come together"},
		"captured check alone": {`{"checks":["captured"]}`, "come together"},
		"a verdict":            {`{"checks":["verdict"],"want_verdict":"rethink"}`, ""},
		"no such verdict":      {`{"checks":["verdict"],"want_verdict":"maybe"}`, "none of the four verdicts"},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "s"), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "s", "scenario.json"), []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := loadScenarios(dir, "")
		if (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %v, want %q", name, err, c.err)
		}
	}
}

func TestTurnsResume(t *testing.T) {
	t.Parallel()

	if got := (Scenario{Prompt: "a"}).turns(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("one prompt: %q", got)
	}

	if got := (Scenario{Prompts: []string{"a", "b"}}).turns(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("prompts: %q", got)
	}

	def, _, _ := agentctl.Profile{}.Resolve()
	o := options{permission: "acceptEdits", budget: "5"}
	args := claudeArgs("/root", o, "b", def, "sess-1")

	if i := slices.Index(args, "--resume"); i < 0 || args[i+1] != "sess-1" || args[len(args)-1] != "b" {
		t.Errorf("a later turn resumes its session before the prompt: %q", args)
	}
}
