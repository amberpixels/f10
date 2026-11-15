package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Three tasks fixed the same tests-and-docs gap in a.go: the brief names the
// category with their ids, leaves the branch's own task out, and --json
// carries the same neighbors.
func TestHistoryNamesARecurringFinding(t *testing.T) {
	repo := testkit.TracedRepo(t, "DM-9/next", "")
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	t.Chdir(t.TempDir())

	content := ""

	for i, task := range []string{"DM-1", "DM-2", "DM-3", "DM-9"} {
		content += fmt.Sprintf("line %d\n", i) // each task's line still stands at HEAD
		if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		testkit.Git(t, repo, "add", "a.go")
		testkit.Git(t, repo, "commit", "-q", "-m", "work\n\nTask: "+task)
	}

	ctx := t.Context()
	project := ledger.Where(ctx, repo).Project
	at := time.Now().Add(-time.Hour)

	var events []ledger.Event

	for i, task := range []string{"DM-1", "DM-2", "DM-3"} {
		e := testkit.StatEvent(at, "s-"+task, task, ledger.ReviewRoundOpened,
			&ledger.ReviewRoundOpenedPayload{Round: "1", Categories: map[string]int32{"tests and docs": 1}})
		e.ID, e.Project = fmt.Sprintf("e%d", i), project
		events = append(events, e)
	}

	l, err := ledger.Open(ctx, ledger.Path(), time.Second, false)
	if err != nil {
		t.Fatal(err)
	}

	l.Append(ctx, events...)
	_ = l.Close()

	out, err := runApp(t, "-C", repo, "history", "a.go").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if want := "recurring finding: tests and docs in 3 of 3 reviewed (DM-1, DM-2, DM-3)"; !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}

	// a directory reads as the files under it, not as one stale path
	dirOut, err := runApp(t, "-C", repo, "history", ".").outErr()
	if err != nil || !strings.Contains(dirOut, "in 3 of 3 reviewed (DM-1, DM-2, DM-3)") {
		t.Errorf("history . (%v):\n%s", err, dirOut)
	}

	if strings.Contains(out, "DM-9") {
		t.Errorf("the branch's own task is in its history:\n%s", out)
	}

	raw, err := runApp(t, "-C", repo, "history", "a.go", "--json").outErr()
	if err != nil {
		t.Fatal(err)
	}

	var brief struct {
		Neighbors []struct {
			Task string `json:"task"`
		} `json:"neighbors"`
	}

	if err := json.Unmarshal([]byte(raw), &brief); err != nil || len(brief.Neighbors) != 3 {
		t.Errorf("--json neighbors = %+v (%v), want three:\n%s", brief.Neighbors, err, raw)
	}
}

func TestHistoryNeedsAPath(t *testing.T) {
	repo := testkit.TracedRepo(t, "main2", "")

	if err := runApp(t, "-C", repo, "history").err; testkit.ExitCode(err) != 2 {
		t.Errorf("history with no path: %v, want exit 2", err)
	}
}
