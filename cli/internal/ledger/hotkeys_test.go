package ledger

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

// sqlOnly is the provider the release before hot keys ran: its two SQL
// migrations.
func sqlOnly(t *testing.T, db *sql.DB) *goose.Provider {
	t.Helper()

	old := fstest.MapFS{}

	for _, name := range []string{"00001_events.sql", "00002_task_refs.sql"} {
		data, err := fs.ReadFile(migrations, "migrations/"+name)
		if err != nil {
			t.Fatal(err)
		}

		old[name] = &fstest.MapFile{Data: data}
	}

	p, err := goose.NewProvider(goose.DialectSQLite3, db, old)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// A database the previous release made gains the columns, filled for the
// rows it already holds; that release still opens it afterwards.
func TestHotKeysMigrateAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := tempPath(t)

	old, err := open(ctx, path, BusyAsync, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sqlOnly(t, old.db).Up(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := old.db.ExecContext(ctx, `PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}

	if _, err := old.db.ExecContext(ctx, `INSERT INTO events (id, machine, at, kind, source, payload)
		VALUES ('01', 'm', '2026-11-01 10:00:00+00:00', 'judge_verdict', 'artifact', '{"path":"/v/1.md","verdict":"stop"}')`); err != nil {
		t.Fatal(err)
	}

	_ = old.Close()

	l := openT(t, path)
	if got := list(t, path, Filter{Kind: JudgeVerdict, Path: "/v/1.md"}); len(got) != 1 {
		t.Errorf("filtered by path: %+v", got)
	}

	if _, err := sqlOnly(t, l.db).Up(ctx); err != nil {
		t.Errorf("the previous release cannot open a migrated database: %v", err)
	}
}

// The verdict dedup's lookup reads a (kind, p_path) index.
func TestVerdictLookupUsesThePathIndex(t *testing.T) {
	l := openT(t, tempPath(t))

	rows, err := l.db.QueryContext(context.Background(),
		`EXPLAIN QUERY PLAN SELECT * FROM events
			WHERE project = ? AND kind = ? AND p_path = ? ORDER BY id DESC LIMIT 1`,
		"p", JudgeVerdict, "/v/1.md")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var plan []string

	for rows.Next() {
		var id, parent, unused int

		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}

		plan = append(plan, detail)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(strings.Join(plan, "\n"), "events_kind_p_path") {
		t.Errorf("plan:\n%s", strings.Join(plan, "\n"))
	}
}

// Openers racing a fresh database may lose to each other's lock; a failed
// migration rolls back whole, so the next open finishes it.
func TestRacingFirstOpensLeaveAMigratableDatabase(t *testing.T) {
	path := tempPath(t)

	var wg sync.WaitGroup

	for range 4 {
		wg.Go(func() {
			if l, err := Open(context.Background(), path, BusyAsync, false); err == nil {
				_ = l.Close()
			}
		})
	}

	wg.Wait()

	l := openT(t, path)
	if v := userVersion(t, l); v != migrationCount(t) {
		t.Errorf("user_version %d", v)
	}

	if got := list(t, path, Filter{Path: "x"}); len(got) != 0 {
		t.Errorf("filtered %+v", got)
	}
}
