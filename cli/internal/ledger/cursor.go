package ledger

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
)

// A Placed event is a row with its cursor: its place in insertion order,
// which ids do not follow, since a spooled row is inserted after its id is
// minted and other machines mint ids on their own clocks.
type Placed struct {
	Event

	Cursor string
}

// A Tail reads one database's rows in insertion order for a stream, keeping
// its read handle between reads, so a stream polling often opens it once.
type Tail struct {
	path string
	l    *Ledger
}

// Narrow picks the rows a tail sends; an empty field matches every row.
// With a project set, a row with no project (an ask's) goes to Orphan, and
// is dropped when Orphan is nil.
type Narrow struct {
	Project string
	Task    string
	Kinds   []string
	Orphan  func(Event) bool
}

// A Page is one After read: the rows that matched, the cursor of the last
// row read whether it matched or not, and whether more rows may wait.
type Page struct {
	Rows []Placed
	Next string
	Full bool
}

// AfterPage bounds one After, so a stream far behind reads a long ledger a
// page at a time instead of whole.
const AfterPage = 500

// OpenTail is a tail of the database at path; nothing opens until a read,
// and a database that does not exist yet reads as empty.
func OpenTail(path string) *Tail { return &Tail{path: path} }

// Close releases the read handle.
func (t *Tail) Close() error {
	if t.l == nil {
		return nil
	}

	err := t.l.Close()
	t.l = nil

	return err
}

// End is the cursor of the last row inserted, what writers spooled
// included; "0" for an empty or missing database.
func (t *Tail) End(ctx context.Context) (string, error) {
	l, err := t.ledger(ctx)
	if err != nil || l == nil {
		return "0", err
	}

	var last int64
	if err := l.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(rowid), 0) FROM events").Scan(&last); err != nil {
		return "", fmt.Errorf("ledger: %w", err)
	}

	return strconv.FormatInt(last, 10), nil
}

// After reads up to AfterPage rows inserted after cursor ("" for the
// first), in insertion order, narrowed by n.
func (t *Tail) After(ctx context.Context, cursor string, n Narrow) (Page, error) {
	page := Page{Next: cursor}

	after := int64(0)

	if cursor != "" {
		v, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || v < 0 {
			return page, fmt.Errorf("ledger: cursor %q is not one this ledger gave", cursor)
		}

		after = v
	}

	l, err := t.ledger(ctx)
	if err != nil || l == nil {
		return page, err
	}

	return l.after(ctx, after, n, page)
}

// ledger replays the spool, then opens the database once it exists; nil,
// nil while it does not.
func (t *Tail) ledger(ctx context.Context) (*Ledger, error) {
	_ = SyncAt(ctx, t.path) // a failed replay still reads what the database holds

	if t.l != nil {
		return t.l, nil
	}

	l, err := OpenRead(ctx, t.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	t.l = l

	return l, nil
}

// rowsAfter reads the rowid range alone, on the rowid key, so an idle poll
// costs only the rows inserted since; Narrow is applied in Go, because the
// project and kind indexes would make SQLite scan every matching row ever
// written. Rowids follow insertion order and the ledger never deletes, so a
// cursor stays valid.
const rowsAfter = `SELECT rowid, id, project, task, kind FROM events WHERE rowid > ? ORDER BY rowid LIMIT ?`

// takes reports whether a row's columns pass n, and whether it is an orphan:
// a row with no project under a project filter, which Orphan decides.
func (n Narrow) takes(project, task, kind string) (bool, bool) {
	if n.Task != "" && task != n.Task {
		return false, false
	}

	if len(n.Kinds) > 0 && !slices.Contains(n.Kinds, kind) {
		return false, false
	}

	if n.Project == "" || project == n.Project {
		return true, false
	}

	return project == "" && n.Orphan != nil, project == ""
}

func (l *Ledger) after(ctx context.Context, after int64, n Narrow, page Page) (Page, error) {
	rows, err := l.db.QueryContext(ctx, rowsAfter, after, AfterPage)
	if err != nil {
		return page, fmt.Errorf("ledger: %w", err)
	}
	defer rows.Close()

	var (
		ids      []string
		cursors  = map[string]string{}
		orphans  = map[string]bool{}
		rowCount int
	)

	for rows.Next() {
		var (
			rowid                   int64
			id, project, task, kind string
		)

		if err := rows.Scan(&rowid, &id, &project, &task, &kind); err != nil {
			return page, fmt.Errorf("ledger: %w", err)
		}

		rowCount++
		page.Next = strconv.FormatInt(rowid, 10)

		take, orphan := n.takes(project, task, kind)
		if !take {
			continue
		}

		ids, cursors[id] = append(ids, id), page.Next
		orphans[id] = orphan
	}

	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("ledger: %w", err)
	}

	page.Full = rowCount == AfterPage

	if len(ids) == 0 {
		return page, nil
	}

	events, err := l.List(ctx, Filter{IDs: ids})
	if err != nil {
		return page, err
	}

	byID := make(map[string]Event, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}

	for _, id := range ids {
		e := byID[id]
		if orphans[id] && !n.Orphan(e) {
			continue
		}

		page.Rows = append(page.Rows, Placed{Event: e, Cursor: cursors[id]})
	}

	return page, nil
}
