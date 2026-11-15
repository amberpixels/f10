package ledger

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
)

// A Placed event is a row with its cursor: its place in insertion order,
// which ids do not follow, since a spooled row is inserted after its id is
// minted and other machines mint ids on their own clocks.
type Placed struct {
	Event

	Cursor string
}

// ReadAfter is up to AfterPage rows inserted after cursor ("" for the first)
// in the database at path, narrowed by project and task when set, in
// insertion order, what writers spooled included. A missing database has none.
func ReadAfter(ctx context.Context, path, cursor, project, task string) ([]Placed, error) {
	after := int64(0)

	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("ledger: cursor %q is not one this ledger gave", cursor)
		}

		after = n
	}

	_ = SyncAt(ctx, path) // a failed replay still reads what the database holds

	l, err := OpenRead(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer l.Close()

	return l.after(ctx, after, project, task)
}

// rowsAfter reads rowids, which SQLite assigns in insertion order; an empty
// project or task matches every row.
const rowsAfter = `SELECT rowid, id FROM events
WHERE rowid > ? AND (? = '' OR project = ?) AND (? = '' OR task = ?)
ORDER BY rowid LIMIT ?`

// AfterPage bounds one ReadAfter, so a stream from the first row reads a
// long ledger a page at a time instead of whole.
const AfterPage = 500

func (l *Ledger) after(ctx context.Context, after int64, project, task string) ([]Placed, error) {
	rows, err := l.db.QueryContext(ctx, rowsAfter, after, project, project, task, task, AfterPage)
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	defer rows.Close()

	var (
		ids     []string
		cursors = map[string]string{}
	)

	for rows.Next() {
		var (
			rowid int64
			id    string
		)

		if err := rows.Scan(&rowid, &id); err != nil {
			return nil, fmt.Errorf("ledger: %w", err)
		}

		ids, cursors[id] = append(ids, id), strconv.FormatInt(rowid, 10)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	if len(ids) == 0 {
		return nil, nil
	}

	events, err := l.List(ctx, Filter{IDs: ids})
	if err != nil {
		return nil, err
	}

	byID := make(map[string]Event, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}

	out := make([]Placed, 0, len(ids))
	for _, id := range ids {
		out = append(out, Placed{Event: byID[id], Cursor: cursors[id]})
	}

	return out, nil
}
