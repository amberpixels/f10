package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A rider is read through its bundle's lead: the plan and the demo report
// are keyed by the lead, so naming the rider finds them.
func TestPlanAndDemoFollowARiderToItsLead(t *testing.T) {
	repo := tracedRepo(t, "DM-1/thing", "")
	git(t, repo, "config", "branch.DM-1/thing.f10-tasks", "DM-2")
	t.Chdir(repo)

	for path, body := range map[string]string{
		filepath.Join(repo, ".f10", "plans", "DM-1.md"):            "# DM-1 plan\n",
		filepath.Join(repo, ".f10", "demo", "DM-1", "report.html"): "<html></html>",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if out, err := runApp(t, "plan", "read", "DM-2").outErr(); err != nil || !strings.Contains(out, "DM-1 plan") {
		t.Errorf("plan read DM-2 = %q, %v; want the lead's plan", out, err)
	}

	if out, err := runApp(t, "demo", "open", "--link", "DM-2").outErr(); err != nil ||
		!strings.Contains(out, filepath.Join("DM-1", "report.html")) {
		t.Errorf("demo open DM-2 = %q, %v; want the lead's report", out, err)
	}
}
