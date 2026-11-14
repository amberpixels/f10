package ledger

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// The raw reads; an IN list is %s, filled by in.
const (
	sessionsSince    = "SELECT DISTINCT session FROM events WHERE project = ? AND at >= ? AND session != ''"
	sessionsOfTasks  = "SELECT DISTINCT session FROM events WHERE project = ? AND task IN (%s) AND session != ''"
	looseRows        = "SELECT id FROM events WHERE project = ? AND task IN (%s) AND session = ''"
	tasksOfSessions  = "SELECT DISTINCT task FROM events WHERE session IN (%s) AND task != ''"
	latestVerdictIDs = "SELECT max(id) FROM events WHERE task = ? AND kind = ? GROUP BY p_round, p_finding"
)

// A Scope is the sessions a read covers. Step timing pairs each session's
// steps with its waits, and a wait may carry no task or another one, so a
// reader needs whole sessions and nothing outside them.
type Scope struct {
	Project string
	Tasks   []string  // the sessions that recorded any of these tasks
	Since   time.Time // the sessions active since, plus every session of their tasks
}

// TaskScope is every session of the project that recorded one of tasks.
func TaskScope(project string, tasks ...string) Scope {
	return Scope{Project: project, Tasks: tasks}
}

// SinceScope is every session of the project active at or after cutoff,
// and every session of the tasks those sessions carry: a task active
// lately is measured over its whole history.
func SinceScope(project string, cutoff time.Time) Scope {
	return Scope{Project: project, Since: cutoff}
}

// Sessions is the distinct sessions s covers.
func (l *Ledger) Sessions(ctx context.Context, s Scope) ([]string, error) {
	sessions, _, err := l.resolve(ctx, s)

	return sessions, err
}

// resolve is the sessions s covers and the tasks it names or found.
func (l *Ledger) resolve(ctx context.Context, s Scope) ([]string, []string, error) {
	tasks := s.Tasks

	var recent []string

	if !s.Since.IsZero() {
		var err error

		recent, err = l.strings(ctx, sessionsSince, s.Project, s.Since.UTC())
		if err != nil {
			return nil, nil, err
		}

		if tasks, err = l.tasksOf(ctx, recent); err != nil {
			return nil, nil, err
		}
	}

	if len(tasks) == 0 {
		return recent, nil, nil
	}

	of, err := l.strings(ctx, in(sessionsOfTasks, len(tasks)), append([]any{s.Project}, anys(tasks)...)...)
	if err != nil {
		return nil, nil, err
	}

	return union(recent, of), tasks, nil
}

// ScopeEvents is every event of the sessions s covers, oldest first, and
// the rows of its tasks written outside any session: finish run by the
// daemon records a task's size with no session.
func (l *Ledger) ScopeEvents(ctx context.Context, s Scope) ([]Event, error) {
	sessions, tasks, err := l.resolve(ctx, s)
	if err != nil {
		return nil, err
	}

	events, err := l.List(ctx, Filter{Sessions: nonNil(sessions)})
	if err != nil || len(tasks) == 0 {
		return events, err
	}

	ids, err := l.strings(ctx, in(looseRows, len(tasks)), append([]any{s.Project}, anys(tasks)...)...)
	if err != nil || len(ids) == 0 {
		return events, err
	}

	loose, err := l.List(ctx, Filter{IDs: ids})
	if err != nil {
		return nil, err
	}

	events = append(events, loose...)
	slices.SortStableFunc(events, func(a, b Event) int { return strings.Compare(a.ID, b.ID) })

	return events, nil
}

// ReadScope is ScopeEvents in the database at path, what writers spooled
// included. A missing database has no events.
func ReadScope(ctx context.Context, path string, s Scope) ([]Event, error) {
	_ = SyncAt(ctx, path) // a failed replay still reads what the database holds

	l, err := OpenRead(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer l.Close()

	return l.ScopeEvents(ctx, s)
}

// tasksOf is the non-empty tasks the sessions recorded.
func (l *Ledger) tasksOf(ctx context.Context, sessions []string) ([]string, error) {
	if len(sessions) == 0 {
		return nil, nil
	}

	return l.strings(ctx, in(tasksOfSessions, len(sessions)), anys(sessions)...)
}

// latestVerdicts is the task's newest finding_verdict row per round and
// finding, oldest first.
func (l *Ledger) latestVerdicts(ctx context.Context, task string) ([]Event, error) {
	ids, err := l.strings(ctx, latestVerdictIDs, task, FindingVerdict)
	if err != nil || len(ids) == 0 {
		return nil, err
	}

	return l.List(ctx, Filter{IDs: ids})
}

func (l *Ledger) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	defer rows.Close()

	var out []string

	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("ledger: %w", err)
		}

		out = append(out, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}

	return out, nil
}

// in fills a query's IN list with n placeholders.
func in(query string, n int) string {
	return fmt.Sprintf(query, strings.TrimSuffix(strings.Repeat("?,", n), ","))
}

func anys(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}

	return out
}

// nonNil keeps an empty lookup empty: a nil Sessions filter matches every row.
func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}

	return ss
}

func union(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)

	return slices.Compact(out)
}
