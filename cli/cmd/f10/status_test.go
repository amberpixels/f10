package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A bare status on a task's branch prints what `status <id>` prints: the
// task's own run with its gates, not every run in the checkout.
func TestBareStatusReadsTheBranchTask(t *testing.T) {
	repo := testkit.TracedRepo(t, "DM-5/fix-retry", "")
	testkit.Git(t, repo, "remote", "remove", "origin") // no host CLI: nothing leaves the machine

	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Chdir(repo)

	for _, r := range []*state.Run{
		{Session: "own", Task: "DM-5", Root: repo, Capture: "prior", Plan: "done", Ship: "running", Leaf: "implement"},
		{Session: "other", Task: "DM-7", Root: repo, Capture: "done", Plan: "running", Ship: "upcoming"},
	} {
		if err := r.Save(t.Context(), dir, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	bare := runStatusJSON(t)
	byID := runStatusJSON(t, "DM-5")

	if len(bare) != 1 || bare[0].Task != "DM-5" || len(bare[0].Gates) == 0 {
		t.Errorf("bare status = %+v, want DM-5's run alone, with its gates", bare)
	}

	if len(bare) != len(byID) || len(bare) > 0 && len(bare[0].Gates) != len(byID[0].Gates) {
		t.Errorf("bare status = %+v, status DM-5 = %+v; want the same", bare, byID)
	}
}

// A guessed task with no branch here leaves the bare answer as it was:
// the session's own run.
func TestBareStatusKeepsTheSessionRunForATaskWithNoBranch(t *testing.T) {
	repo := testkit.TracedRepo(t, "main", "")
	testkit.Git(t, repo, "remote", "remove", "origin") // no host CLI: nothing leaves the machine

	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")
	t.Chdir(repo)

	r := &state.Run{Session: "sess-1", Task: "DM-9", Root: repo, Capture: "done", Plan: "running", Ship: "upcoming"}
	if err := r.Save(t.Context(), dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	got := runStatusJSON(t)
	if len(got) != 1 || got[0].Session != "sess-1" {
		t.Errorf("bare status = %+v, want the session's run", got)
	}
}

func runStatusJSON(t *testing.T, args ...string) []statusview.RunJSON {
	t.Helper()

	r := runApp(t, append([]string{"status", "--json"}, args...)...)
	if r.err != nil {
		t.Fatalf("status %v: %v\n%s", args, r.err, r.combined)
	}

	var runs []statusview.RunJSON
	if err := json.Unmarshal([]byte(r.combined), &runs); err != nil {
		t.Fatalf("status %v: not a run list: %v\n%s", args, err, r.combined)
	}

	return runs
}
