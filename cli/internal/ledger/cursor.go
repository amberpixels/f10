package ledger

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
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

// afterQuery reads rowids, which SQLite assigns in insertion order and the
// ledger never deletes, so a cursor stays valid.
func afterQuery(n Narrow) (string, []any) {
	var (
		q    strings.Builder
		args = []any{}
	)

	q.WriteString("SELECT rowid, id, project FROM events WHERE rowid > ?")

	if n.Project != "" {
		q.WriteString(" AND (project = ? OR project = '')")
		args = append(args, n.Project)
	}

	if n.Task != "" {
		q.WriteString(" AND task = ?")
		args = append(args, n.Task)
	}

	if len(n.Kinds) > 0 {
		q.WriteString(" AND kind IN (?")
		q.WriteString(strings.Repeat(", ?", len(n.Kinds)-1))
		q.WriteString(")")

		for _, k := range n.Kinds {
			args = append(args, k)
		}
	}

	q.WriteString(" ORDER BY rowid LIMIT ?")

	return q.String(), append(args, AfterPage)
}

func (l *Ledger) after(ctx context.Context, after int64, n Narrow, page Page) (Page, error) {
	q, args := afterQuery(n)

	rows, err := l.db.QueryContext(ctx, q, append([]any{after}, args...)...)
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
			rowid       int64
			id, project string
		)

		if err := rows.Scan(&rowid, &id, &project); err != nil {
			return page, fmt.Errorf("ledger: %w", err)
		}

		rowCount++
		page.Next = strconv.FormatInt(rowid, 10)
		ids, cursors[id] = append(ids, id), page.Next

		if n.Project != "" && project == "" {
			orphans[id] = true
		}
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
		if orphans[id] && (n.Orphan == nil || !n.Orphan(e)) {
			continue
		}

		page.Rows = append(page.Rows, Placed{Event: e, Cursor: cursors[id]})
	}

	return page, nil
}
