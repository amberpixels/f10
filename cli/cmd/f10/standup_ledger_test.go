package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A plan saved this week with no commit puts its task in the report, and
// the report says its ledger half is this machine's.
func TestStandupReadsTheLedger(t *testing.T) {
	testkit.IsolateEnv(t)
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	repo := testkit.ScopeRepo(t, testkit.Canonical(t, t.TempDir()), "svc")
	ctx := t.Context()
	place := ledger.Where(ctx, repo)

	e := testkit.StatEvent(
		time.Now().Add(-time.Hour),
		"s1",
		"XY-5",
		ledger.PlanSaved,
		&ledger.PlanSavedPayload{Task: "XY-5"},
	)
	e.ID, e.Project, e.Checkout, e.Branch = "e1", place.Project, place.Checkout, "XY-5/plan"

	l, err := ledger.Open(ctx, ledger.Path(), time.Second, false)
	if err != nil {
		t.Fatal(err)
	}

	l.Append(ctx, e)
	_ = l.Close()

	s := standupJSON(t, "-C", repo)

	if s.Ledger == nil || s.Ledger.Scope != "this machine only" || !s.Ledger.Contributed {
		t.Fatalf("ledger note = %+v", s.Ledger)
	}

	var found bool

	for _, p := range s.Projects {
		for _, task := range p.Tasks {
			if task.ID == "XY-5" && len(task.Activity) == 1 && task.Activity[0].Text == "plan saved" {
				found = true
			}
		}
	}

	if !found {
		t.Errorf("XY-5 is not in the report with its plan: %+v", s.Projects)
	}
}
