package ledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

// The synthetic ledger: 50 projects of 10,000 events, sessions of 200
// rows, each task worked over three sessions.
const (
	budgetProjects    = 50
	budgetPerProject  = 10_000
	budgetSessionRows = 200
	budgetTaskSession = 3
)

// seedBudget writes the synthetic ledger in one transaction: Append's row
// at a time would take minutes.
func seedBudget(t *testing.T, l *Ledger) {
	t.Helper()

	ctx := context.Background()

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events
		(id, machine, at, project, session, task, kind, source, payload) VALUES (?, 'm', ?, ?, ?, ?, ?, 'observed', ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()

	kinds := []string{RunStarted, StateSet, WaitStarted, WaitEnded, SessionStopped, Pushed, TaskFinished}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0

	for i := range budgetPerProject {
		for p := range budgetProjects {
			session := i / budgetSessionRows
			task := session / budgetTaskSession
			kind := kinds[n%len(kinds)]

			payload := "{}"
			if kind == SessionStopped {
				payload = `{"phase":"ship","status":"running"}`
			}

			if _, err := stmt.ExecContext(ctx, fmt.Sprintf("01J%023d", n), t0.Add(time.Duration(n)*time.Second),
				fmt.Sprintf("p%d", p), fmt.Sprintf("p%d-s%d", p, session), fmt.Sprintf("FT-%d", task),
				kind, payload); err != nil {
				t.Fatal(err)
			}

			n++
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// median is how long f takes, the middle of seven runs.
func median(t *testing.T, f func() error) time.Duration {
	t.Helper()

	runs := make([]time.Duration, 7)
	for i := range runs {
		start := time.Now()
		if err := f(); err != nil {
			t.Fatal(err)
		}

		runs[i] = time.Since(start)
	}

	slices.Sort(runs)

	return runs[len(runs)/2]
}

// The reads stats, status and the Stop hook make stay in budget on a large
// ledger. Run by `just bench-ledger`: the seed takes too long for every test run.
func TestReadBudgets(t *testing.T) {
	if os.Getenv("F10_BENCH_LEDGER") != "1" {
		t.Skip("set F10_BENCH_LEDGER=1, or run just bench-ledger")
	}

	ctx := context.Background()
	path := tempPath(t)
	l := openT(t, path)
	seedBudget(t, l)

	task := TaskScope("p7", "FT-10")

	for name, c := range map[string]struct {
		budget time.Duration
		read   func() error
	}{
		"stats <task>": {50 * time.Millisecond, func() error {
			events, err := ReadScope(ctx, path, task)
			if err == nil && len(events) != budgetSessionRows*budgetTaskSession {
				err = fmt.Errorf("read %d rows", len(events))
			}

			return err
		}},
		"status <task>": {50 * time.Millisecond, func() error {
			if _, err := ReadScope(ctx, path, task); err != nil {
				return err
			}

			_, err := ReadEvents(ctx, path, Filter{Project: "p7", Kind: TaskFinished})

			return err
		}},
		"sameStop": {5 * time.Millisecond, func() error {
			_, ok, err := l.Last(ctx, Filter{Session: "p7-s30", Kind: SessionStopped})
			if err == nil && !ok {
				err = errors.New("no stop row")
			}

			return err
		}},
	} {
		if got := median(t, c.read); got > c.budget {
			t.Errorf("%s: %s, budget %s", name, got, c.budget)
		} else {
			t.Logf("%s: %s, budget %s", name, got, c.budget)
		}
	}
}
