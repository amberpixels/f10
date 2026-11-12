package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// TracedRepo is a checkout on the given branch, its project.md declaring
// DM task ids and the traces switch as asked ("" leaves it undeclared).
func TracedRepo(t *testing.T, branch, traces string) string {
	t.Helper()
	IsolateEnv(t)
	t.Setenv("F10_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	repo := MkRepo(t)
	Git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	Git(t, repo, "checkout", "-q", "-b", branch)

	block := "---\ntracker:\n  id: DM-###\n"
	if traces != "" {
		block += "traces: " + traces + "\n"
	}

	path := filepath.Join(repo, ".f10", "instructions", "project.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(block+"---\n# demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return repo
}
