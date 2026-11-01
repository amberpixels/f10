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
		"intent":   {pass: true},
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

	for _, name := range []string{"plan", "intent", "verify", "verdict", "findings"} {
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

	root := filepath.Join("..", "..", "..", "evals", "scenarios")

	for _, s := range list {
		if s.Task == "" || s.Prompt == "" {
			t.Errorf("%s: no task or prompt", s.Name)
		}

		if s.Fixture != "" {
			if _, err := os.Stat(filepath.Join(root, s.Fixture, "repo")); err != nil {
				t.Errorf("%s: fixture %s: %v", s.Name, s.Fixture, err)
			}
		}

		if s.Change != "" {
			if _, err := os.Stat(filepath.Join(root, s.Name, s.Change)); err != nil {
				t.Errorf("%s: change %s: %v", s.Name, s.Change, err)
			}
		}
	}
}

// planted finds each defect by a finding inside its line range, and bounds
// the findings outside every range. The canned round has main.go:3 and :40.
func TestPlantedCheck(t *testing.T) {
	dir := canned(t)
	zero := 0

	for name, c := range map[string]struct {
		s      Scenario
		pass   bool
		detail string
	}{
		"found": {
			s:      Scenario{Task: "T-1", Planted: []Planted{{File: "main.go", From: 1, To: 5}}},
			pass:   true,
			detail: "1 of 1 planted found, 1 outside",
		},
		"missed": {
			s:      Scenario{Task: "T-1", Planted: []Planted{{File: "main.go", From: 1, To: 5}, {File: "main.go", From: 10, To: 12, What: "leak"}}},
			detail: "missed main.go:10-12 (leak)",
		},
		"too many outside": {
			s:      Scenario{Task: "T-1", Planted: []Planted{{File: "main.go", From: 1, To: 5}}, MaxExtra: &zero},
			detail: "1 findings outside the planted defects, at most 0 allowed",
		},
		"no findings": {
			s:      Scenario{Task: "T-2", Planted: []Planted{{File: "main.go", From: 1, To: 5}}},
			detail: "no findings file",
		},
	} {
		got := checkPlanted(t.Context(), dir, c.s)
		if got.Pass != c.pass || !strings.Contains(got.Detail, c.detail) {
			t.Errorf("%s = %+v, want pass %v with %q", name, got, c.pass, c.detail)
		}
	}
}

// A scenario's change lands as a commit on the task's branch, so a review
// run sees a diff against main.
func TestApplyChange(t *testing.T) {
	dir := canned(t)
	patch := filepath.Join(t.TempDir(), "change.patch")

	body := "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n package main\n \n-func main() {}\n+func main() { println(1) }\n"
	if err := os.WriteFile(patch, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := applyChange(t.Context(), dir, "EV-9", patch); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("git", "-C", dir, "log", "--format=%D %s", "main..HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "EV-9/change") || !strings.Contains(string(out), "EV-9: change") {
		t.Errorf("log = %q, want one commit on EV-9/change", out)
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
