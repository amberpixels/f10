package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
)

// editingClaude is a claude that adds a file and changes a tracked one in the
// run's repo, then prints a one-turn stream.
func editingClaude(t *testing.T) {
	t.Helper()

	bin := t.TempDir()
	script := `#!/bin/sh
echo new > added.txt
echo more >> README
echo '{"type":"assistant","message":{"id":"m","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t","name":"Write","input":{"file_path":"added.txt"}}]}}'
echo '{"type":"result","total_cost_usd":0.1,"duration_ms":7,"result":"done","session_id":"s"}'
`

	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

//nolint:paralleltest // editingClaude sets PATH
func TestRunFolder(t *testing.T) {
	editingClaude(t)

	root := turnsRoot(t)
	scenario := filepath.Join(root, "evals", "scenarios", "s")

	for path, body := range map[string]string{
		filepath.Join(scenario, "scenario.json"):                             `{"prompt":"go"}`,
		filepath.Join(scenario, "repo", "tasks", "EV-1.md"):                  "# EV-1\n",
		filepath.Join(scenario, "repo", "f10", "instructions", "project.md"): "---\n---\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	folder := filepath.Join(t.TempDir(), "s")
	s := Scenario{Name: "s", Task: "EV-1", Prompt: "go"}
	def, _, _ := agentctl.Profile{}.Resolve()

	rep := runScenario(
		t.Context(),
		root,
		s,
		options{permission: "acceptEdits", budget: "1"},
		def,
		runOut{folder: folder},
	)
	if rep.Error != "" || rep.RunDir != "s" {
		t.Fatalf("report %+v, want no error and a relative run_dir", rep)
	}

	for _, p := range []string{
		transcriptFile, timesFile, diffFile, "inputs/scenario.json", "inputs/task.md", "inputs/project.md",
		"before/README", "after/README", "after/added.txt", "after/.f10/instructions/project.md",
	} {
		if _, err := os.Stat(filepath.Join(folder, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}

	for _, p := range []string{"before/added.txt", "before/.git", "after/.git"} {
		if _, err := os.Stat(filepath.Join(folder, p)); err == nil {
			t.Errorf("%s should not exist", p)
		}
	}

	patch, _ := os.ReadFile(filepath.Join(folder, diffFile))
	if !bytes.Contains(patch, []byte("b/added.txt")) || !bytes.Contains(patch, []byte("+more")) {
		t.Errorf("diff misses the untracked file or the edit:\n%s", patch)
	}

	if bytes.Contains(patch, []byte(".f10/")) {
		t.Error("the diff holds .f10/, which after/ carries")
	}

	lines, _ := os.ReadFile(filepath.Join(folder, transcriptFile))
	times, _ := os.ReadFile(filepath.Join(folder, timesFile))

	if n := strings.Count(string(lines), "\n"); n != 2 || strings.Count(string(times), "\n") != n {
		t.Errorf("%d transcript lines, times %q", n, times)
	}
}

// A run that errors in setup still keeps its inputs, where its cause is read.
//
//nolint:paralleltest // editingClaude sets PATH
func TestRunFolderOnSetupError(t *testing.T) {
	editingClaude(t)

	root := turnsRoot(t)
	folder := filepath.Join(t.TempDir(), "s")
	s := Scenario{
		Name:    "s",
		Task:    "EV-1",
		Prompt:  "go",
		Branch:  "x",
		History: []HistoryCommit{{File: "missing", Append: "x"}},
	}
	def, _, _ := agentctl.Profile{}.Resolve()

	rep := runScenario(t.Context(), root, s, options{}, def, runOut{folder: folder})
	if rep.Error == "" || len(rep.Checks) != 0 {
		t.Fatalf("report %+v, want a setup error with no checks", rep)
	}

	if _, err := os.Stat(filepath.Join(folder, inputsDir)); err != nil {
		t.Errorf("inputs: %v", err)
	}
}
