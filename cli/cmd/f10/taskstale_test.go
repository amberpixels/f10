package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// task stale takes the branch's task and stale_after from project.md, and
// --created stands in for a tracker the host does not answer.
func TestTaskStaleCommand(t *testing.T) {
	repo := testkit.TracedRepo(t, "DM-5/fix", "")
	t.Chdir(repo)

	run := func(staleAfter string, args ...string) string {
		t.Helper()

		md := "---\ntracker:\n  id: DM-###\n"
		if staleAfter != "" {
			md += "stale_after: " + staleAfter + "\n"
		}

		path := filepath.Join(repo, ".f10", "instructions", "project.md")
		if err := os.WriteFile(path, []byte(md+"---\n# demo\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		r := runApp(t, append([]string{"task", "stale"}, args...)...)
		if r.err != nil {
			t.Fatalf("task stale %v: %v\n%s", args, r.err, r.combined)
		}

		return strings.TrimSpace(r.combined)
	}

	if got := run(""); got != taskcmd.StaleFresh {
		t.Errorf("undeclared = %q", got)
	}

	if got := run("3w", "--created", time.Now().UTC().Format(time.DateOnly)); got != taskcmd.StaleFresh {
		t.Errorf("created today = %q", got)
	}

	if got := run("3w", "--created", "2020-01-01"); got != taskcmd.StaleNeeds {
		t.Errorf("old = %q", got)
	}

	testkit.WriteVerdict(t, filepath.Join(repo, ".f10"), "DM-5", "1", "task DM-5", "proceed", time.Now())

	if got := run("3w", "--created", "2020-01-01"); !strings.HasPrefix(got, taskcmd.StaleJudged+" ") ||
		!strings.HasSuffix(got, filepath.Join("verdicts", "DM-5", "1.md")) {
		t.Errorf("judged = %q", got)
	}
}
