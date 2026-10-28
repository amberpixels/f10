// Package ledger is f10's local record of what runs did: one append-only
// events table in ~/.f10/f10.db, outside every repo, so stealth is
// unaffected. Hooks write what they observe, `f10 state` what the model
// claims, and artifacts (plans, findings) what they hold. Beside it,
// task_refs keeps each task's last-read title and url, overwritten in place.
//
// A ledger write never fails its caller: hooks and `f10 state` append their
// rows to a spool file beside the database, which SessionStart, Stop and
// every reader replay into it.
package ledger

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/amberpixels/r3"
	r3sqlite3 "github.com/amberpixels/r3/drivers/sqlite3"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // the cgo-free database/sql driver, registered as "sqlite"
)

// PathEnv overrides the database's path; the spool and machine id live
// beside it, so a test pointing it at a temp dir touches nothing else.
const PathEnv = "F10_LEDGER"

// Busy timeouts: under the 5s hook timeout for async writers, so a contended
// write spools instead of the hook being killed; short for a synchronous
// hook, so a busy ledger never delays a skill's start.
const (
	BusyAsync = 3 * time.Second
	BusySync  = 200 * time.Millisecond
)

// Sources: what the hook saw, what an artifact holds, what the model said.
const (
	Observed = "observed"
	Artifact = "artifact"
	Claimed  = "claimed"
)

// Payload is an event's kind-specific detail, stored as JSON.
type Payload = map[string]any

type payloadCol = r3.JSONColumn[Payload]

func col(p Payload) payloadCol { return r3.NewJSONColumn(p) }

// NewPayload wraps a payload for an Event.
func NewPayload(p Payload) r3.JSONColumn[Payload] { return col(p) }

// Event is one row. Every column but the id is immutable, so r3 has no update
// path for it and an upsert of a known id does nothing: the table only grows.
type Event struct {
	ID           string     `db:"id,pk"                   json:"id"`
	Machine      string     `db:"machine,immutable"       json:"machine"`
	At           time.Time  `db:"at,immutable"            json:"at"`
	Project      string     `db:"project,immutable"       json:"project"`
	ProjectLocal bool       `db:"project_local,immutable" json:"project_local"`
	Checkout     string     `db:"checkout,immutable"      json:"checkout"`
	Branch       string     `db:"branch,immutable"        json:"branch"`
	Session      string     `db:"session,immutable"       json:"session"`
	Task         string     `db:"task,immutable"          json:"task"`
	Kind         string     `db:"kind,immutable"          json:"kind"`
	Source       string     `db:"source,immutable"        json:"source"`
	Payload      payloadCol `db:"payload,immutable"       json:"payload"`
}

func (Event) TableName() string { return "events" }

//go:embed migrations/*.sql
var migrations embed.FS

// Path is the database file: F10_LEDGER, else ~/.f10/f10.db.
func Path() string {
	if p := os.Getenv(PathEnv); p != "" {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".f10", "f10.db")
}

// A Ledger is an open database.
type Ledger struct {
	db   *sql.DB
	repo *r3sqlite3.Sqlite3CRUD[Event, string]
	path string
}

// Open opens the database at path for writing: it creates the file and runs
// the migrations. With replay it also replays the spool, which a synchronous
// hook leaves to the next async writer: a backlog has no bound.
func Open(ctx context.Context, path string, busy time.Duration, replay bool) (*Ledger, error) {
	if path == "" {
		return nil, errors.New("ledger: no path")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	l, err := open(ctx, path, busy, "")
	if err != nil {
		return nil, err
	}

	if err := l.migrate(ctx); err != nil {
		_ = l.Close()

		return nil, err
	}

	if replay {
		l.replay(ctx)
	}

	return l, nil
}

// OpenRead opens an existing database read-only: no migration, no replay, so
// reading never writes. A missing file is fs.ErrNotExist.
func OpenRead(ctx context.Context, path string) (*Ledger, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	return open(ctx, path, BusyAsync, "&mode=ro")
}

func open(ctx context.Context, path string, busy time.Duration, extra string) (*Ledger, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)%s",
		path, busy.Milliseconds(), extra)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("ledger: %w", err)
	}

	cfg := r3.DefaultConfig()
	cfg.Defaults.Unpaginated = true

	return &Ledger{db: db, repo: r3sqlite3.NewSqlite3CRUD[Event, string](db, r3.WithConfig(cfg)), path: path}, nil
}

// Close closes the database.
func (l *Ledger) Close() error { return l.db.Close() }

// Append writes events, ids and machine filled in where empty. An event the
// database refuses goes to the spool with the ones after it.
func (l *Ledger) Append(ctx context.Context, events ...Event) {
	fill(l.path, events)

	for i, e := range events {
		if err := l.insert(ctx, e); err != nil {
			spool(l.path, events[i:])

			return
		}
	}
}

// Filter narrows List; an empty field matches every row.
type Filter struct {
	Project string
	Task    string
	Session string
	Kind    string
}

func (f Filter) filters() r3.Filters {
	var filters r3.Filters

	for column, v := range map[string]string{"project": f.Project, "task": f.Task, "session": f.Session, "kind": f.Kind} {
		if v != "" {
			filters = append(filters, r3.Eq(column, v))
		}
	}

	return filters
}

// List returns the events f matches, oldest first.
func (l *Ledger) List(ctx context.Context, f Filter) ([]Event, error) {
	events, _, err := l.repo.List(ctx, r3.Query{
		Filters:    f.filters(),
		Sorts:      r3.Sorts{r3.NewSortAscSpec(r3.NewFieldSpec("id"))},
		Pagination: r3.Unpaginated(),
	})
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	return events, nil
}

// Count is how many events f matches.
func (l *Ledger) Count(ctx context.Context, f Filter) (int64, error) {
	n, err := l.repo.Count(ctx, r3.Query{Filters: f.filters()})
	if err != nil {
		return 0, fmt.Errorf("ledger: %w", err)
	}

	return n, nil
}

// Record spools events for the next replay point: a write never opens the
// database, so no caller waits on a migration or a busy lock.
func Record(events ...Event) {
	if len(events) == 0 {
		return
	}

	path := Path()
	if path == "" {
		return
	}

	fill(path, events)
	spool(path, events)
}

// Sync replays the spool at Path into the database. A reader calls it
// first, so what writers spooled is read back; with nothing spooled it
// costs a stat.
func Sync(ctx context.Context) error { return SyncAt(ctx, Path()) }

// SyncAt is Sync for the database at path.
func SyncAt(ctx context.Context, path string) error {
	if path == "" || !pending(path) {
		return nil
	}

	l, err := Open(ctx, path, BusyAsync, true)
	if err != nil {
		return err
	}

	return l.Close()
}

// migrate runs goose only when user_version lags the embedded migrations,
// so an open of a current database costs one pragma read.
func (l *Ledger) migrate(ctx context.Context) error {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	files, err := fs.Glob(sub, "*.sql")
	if err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	var version int
	if err := l.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	if version == len(files) {
		return nil
	}

	p, err := goose.NewProvider(goose.DialectSQLite3, l.db, sub)
	if err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("ledger: migrate: %w", err)
	}

	// a pragma takes no bound parameter; the count is ours, not input
	if _, err := l.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(files))); err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	return nil
}

// insert writes one event; a known id is left as stored.
func (l *Ledger) insert(ctx context.Context, e Event) error {
	_, err := l.repo.Upsert(ctx, e)
	if err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	return nil
}
