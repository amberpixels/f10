package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// task regression records what it found as the agent's claim on the bug
// task, prints the plan header's line, and records a claimed origin apart.
func TestTaskRegressionCommand(t *testing.T) {
	repo := testkit.TracedRepo(t, "DM-9/fix", "")
	t.Chdir(repo)
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	if err := os.WriteFile(filepath.Join(repo, "calc.go"), []byte("package calc\n\nvar n = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	testkit.Git(t, repo, "add", "calc.go")
	testkit.Git(t, repo, "commit", "-q", "-m", "set n\n\nTask: DM-4")
	sha := strings.TrimSpace(testkit.Git(t, repo, "rev-parse", "HEAD"))

	got := runApp(t, "task", "regression", "--commit", sha, "--lines", "calc.go:3",
		"--found-by", "blame", "--confidence", "likely", "--repro", "obvious").must(t)
	if strings.TrimSpace(got) != "DM-4 (unreviewed, likely)" {
		t.Errorf("printed %q", got)
	}

	runApp(t, "task", "regression", "--claimed", "DM-4", "--text", "regression of DM-4").must(t)

	// a bug with no cause records its outcome alone, with no confidence to give
	if got := runApp(t, "task", "regression", "--repro", "unconfirmed").must(t); !strings.HasPrefix(got, "none (") {
		t.Errorf("outcome alone printed %q", got)
	}

	if r := runApp(t, "task", "regression", "--repro", "maybe", "--confidence", "likely"); r.err == nil {
		t.Error("an unknown --repro was accepted")
	}

	var kinds []string

	for line := range strings.SplitSeq(strings.TrimSpace(runApp(t, "ledger", "dump", "--json").must(t)), "\n") {
		var e ledger.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%v: %s", err, line)
		}

		if e.Task != "DM-9" || e.Source != ledger.Claimed {
			t.Errorf("%s: task %q source %q", e.Kind, e.Task, e.Source)
		}

		kinds = append(kinds, e.Kind)

		if e.Kind == ledger.RegressionFound && len(kinds) == 1 {
			p, err := ledger.Decode[ledger.RegressionFoundPayload](e)
			if err != nil || p.OriginTask != "DM-4" || p.Repro != "obvious" || p.Lines[0] != "calc.go:3-3" {
				t.Errorf("found %+v %v", p, err)
			}
		}
	}

	if got := strings.Join(kinds, " "); got != "regression_found regression_claimed regression_found" {
		t.Errorf("kinds %q", got)
	}
}
