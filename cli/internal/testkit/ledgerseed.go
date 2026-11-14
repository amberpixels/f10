package testkit

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// The synthetic ledger SeedLedger writes: 50 projects of 10,000 events,
// sessions of 200 rows, each task worked over three sessions. Project p<n>'s
// session s<k> is "p<n>-s<k>" and carries task "FT-<k/3>".
const (
	SeedProjects    = 50
	SeedPerProject  = 10_000
	SeedSessionRows = 200
	SeedTaskSession = 3
)

// SeedLedger migrates a ledger at path and fills it with the synthetic
// rows in one transaction: Append's row at a time would take minutes.
func SeedLedger(tb testing.TB, path string) {
	tb.Helper()

	ctx := context.Background()

	l, err := ledger.Open(ctx, path, ledger.BusyAsync, false)
	if err != nil {
		tb.Fatal(err)
	}

	_ = l.Close()

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		tb.Fatal(err)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		tb.Fatal(err)
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events
		(id, machine, at, project, session, task, kind, source, payload) VALUES (?, 'm', ?, ?, ?, ?, ?, 'observed', ?)`)
	if err != nil {
		tb.Fatal(err)
	}
	defer stmt.Close()

	kinds := []string{
		ledger.RunStarted, ledger.StateSet, ledger.WaitStarted, ledger.WaitEnded,
		ledger.SessionStopped, ledger.Pushed, ledger.TaskFinished,
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0

	for i := range SeedPerProject {
		for p := range SeedProjects {
			session := i / SeedSessionRows
			kind := kinds[n%len(kinds)]

			payload := "{}"
			if kind == ledger.SessionStopped {
				payload = `{"phase":"ship","status":"running"}`
			}

			if _, err := stmt.ExecContext(
				ctx,
				fmt.Sprintf("01J%023d", n),
				t0.Add(time.Duration(n)*time.Second),
				fmt.Sprintf(
					"p%d",
					p,
				),
				fmt.Sprintf("p%d-s%d", p, session),
				fmt.Sprintf("FT-%d", session/SeedTaskSession),
				kind,
				payload,
			); err != nil {
				tb.Fatal(err)
			}

			n++
		}
	}

	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}
