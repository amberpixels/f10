package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// canned is testdata/result as a run would leave it: its f10/ copied to
// .f10/, the way prepare lays out a fixture.
func canned(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := prepare(t.Context(), filepath.Join("testdata", "result"), dir); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestChecksOnACannedRun(t *testing.T) {
	dir := canned(t)
	s := Scenario{Task: "T-1", Verify: []string{"true"}}

	for name, want := range map[string]struct {
		pass   bool
		detail string
	}{
		"plan":     {pass: true},
		"verify":   {pass: true},
		"verdict":  {pass: true},
		"findings": {detail: "main.go:40 does not resolve"},
		"commit":   {detail: "no commit past the initial one"},
	} {
		c := checks[name](t.Context(), dir, s)
		if c.Pass != want.pass || !strings.Contains(c.Detail, want.detail) {
			t.Errorf("%s = %+v, want pass %v with %q", name, c, want.pass, want.detail)
		}
	}
}

func TestChecksFailOnMissingArtifacts(t *testing.T) {
	dir := canned(t)
	s := Scenario{Task: "T-2", Verify: []string{"false"}}

	for _, name := range []string{"plan", "verify", "verdict", "findings"} {
		if c := checks[name](t.Context(), dir, s); c.Pass {
			t.Errorf("%s passed with nothing to check", name)
		}
	}
}

func TestPlanCheckNamesWhatIsMissing(t *testing.T) {
	dir := canned(t)
	plan := filepath.Join(dir, ".f10", "plans", "T-1.md")

	if err := os.WriteFile(plan, []byte("# T-1\n\n## Stages\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := checkPlan(t.Context(), dir, Scenario{Task: "T-1"})
	for _, want := range []string{"Roles:", "## Consequences", "## Gaps"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q does not name %s", c.Detail, want)
		}
	}
}

func TestCommitCheckSeesANewCommit(t *testing.T) {
	dir := canned(t)

	if out, err := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", "work").
		CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}

	if c := checkCommit(t.Context(), dir, Scenario{}); !c.Pass {
		t.Errorf("commit = %+v after a commit", c)
	}
}

// The fixture's .f10/ stays out of its initial commit, as f10 keeps it
// untracked in a stealth project.
func TestPrepareKeepsF10Untracked(t *testing.T) {
	dir := canned(t)

	out, err := exec.Command("git", "-C", dir, "ls-files").Output()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), ".f10") || !strings.Contains(string(out), "main.go") {
		t.Errorf("tracked files = %q", out)
	}
}

// Every scenario in the repo loads, and names only checks that exist.
func TestScenariosLoad(t *testing.T) {
	list, err := loadScenarios(filepath.Join("..", "..", "..", "evals", "scenarios"), "")
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range list {
		if s.Task == "" || s.Prompt == "" {
			t.Errorf("%s: no task or prompt", s.Name)
		}
	}
}

// accept compares each command's stdout with what the scenario wants, and
// fails a scenario that names none: verify alone passes on the fixture.
func TestAcceptCheck(t *testing.T) {
	dir := canned(t)

	for _, c := range []struct {
		accept []Accept
		pass   bool
	}{
		{[]Accept{{Run: "echo HELLO, ADA!", Want: "HELLO, ADA!"}}, true},
		{[]Accept{{Run: "echo Hello, Ada!", Want: "HELLO, ADA!"}}, false},
		{[]Accept{{Run: "exit 1", Want: ""}}, false},
		{nil, false},
	} {
		if got := checkAccept(t.Context(), dir, Scenario{Accept: c.accept}); got.Pass != c.pass {
			t.Errorf("accept %+v = %+v, want pass %v", c.accept, got, c.pass)
		}
	}
}
