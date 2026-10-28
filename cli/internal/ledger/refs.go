package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/amberpixels/r3"
	r3sqlite3 "github.com/amberpixels/r3/drivers/sqlite3"
)

// A TaskRef is a task's title and url as f10 last read them from the
// tracker. Unlike an event it is overwritten by the next read: a renamed
// task stays stale until f10 reads it again.
type TaskRef struct {
	Key     string    `db:"id,pk"             json:"-"`
	Project string    `db:"project,immutable" json:"project"`
	ID      string    `db:"task,immutable"    json:"id"`
	Title   string    `db:"title"             json:"title"`
	URL     string    `db:"url"               json:"url"`
	SeenAt  time.Time `db:"seen_at"           json:"seen_at"`
}

func (TaskRef) TableName() string { return "task_refs" }

func refKey(project, id string) string { return project + " " + id }

func (l *Ledger) refs() *r3sqlite3.Sqlite3CRUD[TaskRef, string] {
	cfg := r3.DefaultConfig()
	cfg.Defaults.Unpaginated = true

	return r3sqlite3.NewSqlite3CRUD[TaskRef, string](l.db, r3.WithConfig(cfg))
}

// Remember writes refs for project, each replacing the row for its id. A ref
// with no title is skipped: it would only hide a later lookup's miss. One
// with no url keeps the stored one, since some reads give a title alone.
func (l *Ledger) Remember(ctx context.Context, project string, refs ...TaskRef) error {
	repo := l.refs()
	now := time.Now().UTC()

	var bare []string

	for _, r := range refs {
		if r.URL == "" {
			bare = append(bare, r.ID)
		}
	}

	stored, err := l.Refs(ctx, project, bare)
	if err != nil {
		return err
	}

	for _, r := range refs {
		if r.URL == "" {
			r.URL = stored[r.ID].URL
		}

		if r.ID == "" || r.Title == "" {
			continue
		}

		r.Project, r.Key = project, refKey(project, r.ID)
		if r.SeenAt.IsZero() {
			r.SeenAt = now
		}

		if _, err := repo.Upsert(ctx, r); err != nil {
			return fmt.Errorf("ledger: %w", err)
		}
	}

	return nil
}

// Refs is the rows project holds for ids, by id; an id never read is absent.
func (l *Ledger) Refs(ctx context.Context, project string, ids []string) (map[string]TaskRef, error) {
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, refKey(project, id))
	}

	rows, _, err := l.refs().List(ctx, r3.Query{
		Filters:    r3.Filters{r3.In("id", keys)},
		Pagination: r3.Unpaginated(),
	})
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	out := make(map[string]TaskRef, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}

	return out, nil
}

// RememberRefs opens the ledger at Path, writes refs and closes it. Like
// Record it never fails its caller, but a ref that cannot be written is
// dropped rather than spooled: the next read of the task rebuilds it.
func RememberRefs(ctx context.Context, project string, refs ...TaskRef) {
	if project == "" || len(refs) == 0 {
		return
	}

	path := Path()
	if path == "" {
		return
	}

	l, err := Open(ctx, path, BusySync, false)
	if err != nil {
		return
	}

	defer l.Close()

	_ = l.Remember(ctx, project, refs...)
}

// LookupRefs reads project's rows for ids, after replaying any spool. A
// missing database, or one no run has migrated to hold refs yet, knows none.
func LookupRefs(ctx context.Context, project string, ids []string) map[string]TaskRef {
	_ = Sync(ctx) // a failed replay still reads what the database holds

	l, err := OpenRead(ctx, Path())
	if err != nil {
		return nil
	}

	defer l.Close()

	refs, err := l.Refs(ctx, project, ids)
	if err != nil {
		return nil
	}

	return refs
}
