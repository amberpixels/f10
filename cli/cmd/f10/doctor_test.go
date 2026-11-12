package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/doctor"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A broken project.md stops every command that acts on facts, but doctor
// runs past it to report it, and exits 1 for it.
func TestDoctorReportsABrokenProjectAndExitsNonZero(t *testing.T) {
	home := testkit.IsolateEnv(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDECODE", "")

	dir := testkit.MkRepo(t)
	testkit.Git(t, dir, "remote", "remove", "origin") // no host CLI call leaves the test

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
	if code := testkit.ExitCode(err); code != 1 {
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

// The user's files are facts, so doctor reports a broken one by its path,
// even in a checkout with no project.md.
func TestDoctorReportsABrokenUserFile(t *testing.T) {
	home := testkit.IsolateEnv(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CLAUDECODE", "")

	user := filepath.Join(home, ".f10")
	t.Setenv(storage.UserDirEnv, user)

	if err := os.MkdirAll(user, 0o750); err != nil {
		t.Fatal(err)
	}

	paces := filepath.Join(user, facts.UserPacesFile)
	if err := os.WriteFile(paces, []byte("---\npaces:\n  bad: { implement: off }\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := testkit.MkRepo(t)
	testkit.Git(t, dir, "remote", "remove", "origin") // no host CLI call leaves the test

	out, _ := runApp(t, "-C", dir, "doctor", "--json").outErr()

	var results []doctor.Result
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("output is not a JSON array of checks: %v\n%s", err, out)
	}

	for _, r := range results {
		if r.Name == doctor.CheckProject {
			if r.Status != doctor.StatusProblem || !strings.Contains(r.Detail, paces) ||
				!strings.Contains(r.Remedy, "edit "+paces) {
				t.Errorf("project check = %q (%s; %s)", r.Status, r.Detail, r.Remedy)
			}

			return
		}
	}

	t.Fatal("no project check in the output")
}
