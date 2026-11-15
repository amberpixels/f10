package statusview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/history"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

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
	path := filepath.Join(t.TempDir(), "f10.db")
	testkit.SeedLedger(t, path)

	l, err := ledger.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// a board of children read at once, then one more task widening the read
	board := []string{"FT-1", "FT-2", "FT-3", "FT-4", "FT-5", "FT-6", "FT-7", "FT-8"}

	for name, c := range map[string]struct {
		budget time.Duration
		read   func() error
	}{
		"stats <task>": {50 * time.Millisecond, func() error {
			events, err := ledger.ReadScope(ctx, path, ledger.TaskScope("p7", "FT-10"))
			if err == nil && len(events) != testkit.SeedSessionRows*testkit.SeedTaskSession {
				err = fmt.Errorf("read %d rows", len(events))
			}

			return err
		}},
		"history <paths>": {100 * time.Millisecond, func() error {
			events, err := history.ReadLedger(ctx, path, "p7", board)
			if err == nil && len(events) == 0 {
				err = errors.New("read no rows")
			}

			return err
		}},
		"status": {50 * time.Millisecond, func() error {
			s := newScopedLedger(path, "p7")
			s.For(ctx, board...)

			if len(s.For(ctx, "FT-10")) == 0 {
				return errors.New("read no rows")
			}

			return nil
		}},
		"sameStop": {5 * time.Millisecond, func() error {
			// the observer's read: the session's stops and starts, for the run context
			rows, err := l.List(ctx, ledger.Filter{
				Session: "p7-s30", Kinds: []string{ledger.SessionStopped, ledger.RunStarted},
			})
			if err == nil && len(rows) == 0 {
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
