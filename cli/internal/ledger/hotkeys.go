package ledger

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// Hot payload keys as generated columns, so a read filters on them with an
// index. A renamed key's version bump changes its column, in its own
// migration, to a COALESCE over the old and new names: old rows keep theirs.
var hotKeyColumns = []struct{ column, key string }{
	{"p_path", "path"},
	{"p_round", "round"},
	{"p_finding", "finding"},
}

// hotKeys is a Go migration: ALTER TABLE ADD COLUMN has no IF NOT EXISTS,
// and two processes can race the first open, so it adds only what is missing.
var hotKeys = goose.NewGoMigration(3, &goose.GoFunc{RunTx: addHotKeys}, nil)

func addHotKeys(ctx context.Context, tx *sql.Tx) error {
	// write first: a read lock upgraded mid-transaction fails at once as
	// busy when another opener writes, where a first write waits its turn
	if _, err := tx.ExecContext(ctx, "UPDATE events SET kind = kind WHERE 0"); err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	have, err := columns(ctx, tx)
	if err != nil {
		return err
	}

	for _, c := range hotKeyColumns {
		if have[c.column] {
			continue
		}

		stmt := fmt.Sprintf(
			"ALTER TABLE events ADD COLUMN %s TEXT GENERATED ALWAYS AS (json_extract(payload, '$.%s')) VIRTUAL",
			c.column,
			c.key,
		)
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ledger: %w", err)
		}
	}

	for _, stmt := range []string{
		"CREATE INDEX IF NOT EXISTS events_kind_p_path ON events (kind, p_path)",
		"CREATE INDEX IF NOT EXISTS events_kind_p_round ON events (kind, p_round, p_finding)",
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ledger: %w", err)
		}
	}

	return nil
}

// columns is the events table's columns, generated ones included.
func columns(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_xinfo('events')")
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	defer rows.Close()

	have := map[string]bool{}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("ledger: %w", err)
		}

		have[name] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	return have, nil
}
