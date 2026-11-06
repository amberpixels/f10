package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/doctor"
)

// A broken project.md stops every command that acts on facts, but doctor
// runs past it to report it, and exits 1 for it.
func TestDoctorReportsABrokenProjectAndExitsNonZero(t *testing.T) {
	home := isolateEnv(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDECODE", "")

	dir := mkRepo(t)
	git(t, dir, "remote", "remove", "origin") // no host CLI call leaves the test

	instr := filepath.Join(dir, ".f10", "instructions")
	if err := os.MkdirAll(instr, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(instr, "project.md"),
		[]byte("---\nbogus: 1\n---\n# demo\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	out, err := runApp(t, "-C", dir, "doctor", "--json").outErr()
	if code := exitCode(err); code != 1 {
		t.Fatalf("exit code %d (%v), want 1", code, err)
	}

	var results []doctor.Result
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("output is not a JSON array of checks: %v\n%s", err, out)
	}

	for _, r := range results {
		if r.Name == doctor.CheckProject {
			if r.Status != doctor.StatusProblem {
				t.Errorf("project check = %q (%s), want problem", r.Status, r.Detail)
			}

			return
		}
	}

	t.Fatal("no project check in the output")
}
