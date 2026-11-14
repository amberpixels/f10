package statusview

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// A status call reads the sessions of the tasks it shows, a bundle rider
// through its lead, and every finish row of the project; never another
// task's sessions.
func TestScopedLedgerReadsOnlyTheShownTasks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "f10.db")
	t0 := time.Unix(1_800_000_000, 0)

	row := func(n int, session, task, branch, kind string, p any) ledger.Event {
		e := event(t0.Add(time.Duration(n)*time.Minute), task, kind, p)
		e.ID, e.Project, e.Session, e.Branch = fmt.Sprintf("01J%023d", n), "p", session, branch

		return e
	}
	claim := ledger.ClaimPayload{Ship: "running", Leaf: "implement"}

	events := []ledger.Event{
		row(1, "s1", "", "FT-1/x", ledger.RunStarted, ledger.RunStartedPayload{Skill: "ship"}),
		row(2, "s1", "FT-1", "FT-1/x", ledger.StateSet, claim),
		row(3, "s2", "FT-5", "FT-5+FT-6/y", ledger.StateSet, claim),
		row(4, "s3", "FT-9", "FT-9/z", ledger.StateSet, claim),
		row(5, "s9", "FT-9", "", ledger.TaskFinished, ledger.TaskFinishedPayload{Args: []string{"FT-7"}}),
	}

	l, err := ledger.Open(ctx, path, ledger.BusyAsync, false)
	if err != nil {
		t.Fatal(err)
	}

	l.Append(ctx, events...)
	_ = l.Close()

	s := newScopedLedger(path, "p")

	ids := func(evs []ledger.Event) []string {
		var out []string
		for _, e := range evs {
			out = append(out, e.Session+":"+e.Kind)
		}

		return out
	}

	got := ids(s.For(ctx, "FT-1"))
	if want := []string{"s1:run_started", "s1:state_set", "s9:task_finished"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("FT-1: %v, want %v", got, want)
	}

	got = ids(s.For(ctx, "FT-6", bundleLead("FT-6", "FT-5+FT-6/y")))
	if want := []string{
		"s1:run_started",
		"s1:state_set",
		"s2:state_set",
		"s9:task_finished",
	}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("FT-6 rides FT-5: %v, want %v", got, want)
	}
}

func TestBundleLead(t *testing.T) {
	for _, c := range []struct{ id, branch, want string }{
		{"FT-6", "FT-5+FT-6/y", "FT-5"},
		{"FT-5", "FT-5+FT-6/y", ""},
		{"FT-6", "FT-6/y", ""},
		{"FT-6", "", ""},
	} {
		if got := bundleLead(c.id, c.branch); got != c.want {
			t.Errorf("bundleLead(%s, %s) = %q, want %q", c.id, c.branch, got, c.want)
		}
	}
}
