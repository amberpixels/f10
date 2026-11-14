package ledger

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// queryPlan is EXPLAIN QUERY PLAN's detail lines for query.
func queryPlan(t *testing.T, l *Ledger, query string, args ...any) []string {
	t.Helper()

	rows, err := l.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
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

	return plan
}

// Every bounded read searches an index; none scans the table or an index whole.
func TestBoundedReadsSearchAnIndex(t *testing.T) {
	l := openT(t, tempPath(t))
	at := time.Now().UTC()

	for name, q := range map[string]struct {
		sql  string
		args []any
	}{
		"last of a session's kind": {
			"SELECT * FROM events WHERE session = ? AND kind = ? ORDER BY id DESC LIMIT 1",
			[]any{"s", SessionStopped},
		},
		"a session set": {"SELECT * FROM events WHERE session IN (?,?) ORDER BY id", []any{"s1", "s2"}},
		"a project's kind": {
			"SELECT * FROM events WHERE project = ? AND kind = ? ORDER BY id",
			[]any{"p", TaskFinished},
		},
		"a round's openings": {
			"SELECT * FROM events WHERE task = ? AND kind = ? AND p_round = ? ORDER BY id",
			[]any{"FT-1", ReviewRoundOpened, "1"},
		},
		"rows by id":                 {"SELECT * FROM events WHERE id IN (?,?) ORDER BY id", []any{"a", "b"}},
		"sessions since":             {sessionsSince, []any{"p", at}},
		"sessions of tasks":          {in(sessionsOfTasks, 2), []any{"p", "FT-1", "FT-2"}},
		"rows outside any session":   {in(looseRows, 2), []any{"p", "FT-1", "FT-2"}},
		"tasks of sessions":          {in(tasksOfSessions, 2), []any{"s1", "s2"}},
		"latest verdict per finding": {latestVerdictIDs, []any{"FT-1", FindingVerdict}},
	} {
		for _, line := range queryPlan(t, l, q.sql, q.args...) {
			if strings.HasPrefix(line, "SCAN") {
				t.Errorf("%s scans: %s", name, line)
			}
		}
	}
}

func TestFilterFieldsAndLast(t *testing.T) {
	ctx := context.Background()
	path := tempPath(t)
	l := openT(t, path)
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

	var events []Event

	for i, r := range []struct{ session, task, kind string }{
		{"s1", "FT-1", SessionStopped},
		{"s1", "FT-1", StateSet},
		{"s2", "FT-2", SessionStopped},
		{"s1", "FT-1", SessionStopped},
		{"s3", "FT-3", Pushed},
	} {
		e := MustEvent(r.kind, mustPayload(r.kind))
		e.ID, e.At, e.Project, e.Session, e.Task = fmt.Sprintf("01J%023d", i), t0.Add(time.Duration(i)*time.Hour), "p",
			r.session, r.task
		events = append(events, e)
	}

	l.Append(ctx, events...)

	sessions := func(f Filter) []string {
		var out []string
		for _, e := range list(t, path, f) {
			out = append(out, e.Session+"/"+e.Kind)
		}

		return out
	}

	for name, c := range map[string]struct {
		f    Filter
		want []string
	}{
		"since":      {Filter{Since: t0.Add(3 * time.Hour)}, []string{"s1/" + SessionStopped, "s3/" + Pushed}},
		"sessions":   {Filter{Sessions: []string{"s2", "s3"}}, []string{"s2/" + SessionStopped, "s3/" + Pushed}},
		"no session": {Filter{Sessions: []string{}}, nil},
		"kinds":      {Filter{Kinds: []string{StateSet, Pushed}}, []string{"s1/" + StateSet, "s3/" + Pushed}},
		"tasks":      {Filter{Tasks: []string{"FT-2"}}, []string{"s2/" + SessionStopped}},
	} {
		if got := sessions(c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}

	e, ok, err := l.Last(ctx, Filter{Session: "s1", Kind: SessionStopped})
	if err != nil || !ok || e.ID != events[3].ID {
		t.Errorf("last: %s %v %v", e.ID, ok, err)
	}

	if _, ok, err := l.Last(ctx, Filter{Session: "none"}); ok || err != nil {
		t.Errorf("last of nothing: %v %v", ok, err)
	}
}

func TestScopes(t *testing.T) {
	ctx := context.Background()
	l := openT(t, tempPath(t))
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

	var events []Event

	for i, r := range []struct {
		day           int
		session, task string
	}{
		{0, "old", "FT-1"},
		{0, "other", "FT-9"},
		{5, "new", ""},
		{5, "new", "FT-1"},
		{5, "", "FT-1"},
		{5, "q", "FT-1"}, // another project
	} {
		e := MustEvent(Pushed, PushedPayload{})
		e.ID, e.At, e.Project, e.Session, e.Task = fmt.Sprintf("01J%023d", i), t0.AddDate(0, 0, r.day), "p",
			r.session, r.task
		if r.session == "q" {
			e.Project = "q"
		}

		events = append(events, e)
	}

	l.Append(ctx, events...)

	for name, c := range map[string]struct {
		s    Scope
		want []string
	}{
		"task":  {TaskScope("p", "FT-1"), []string{"new", "old"}},
		"since": {SinceScope("p", t0.AddDate(0, 0, 3)), []string{"new", "old"}},
		"none":  {TaskScope("p", "FT-404"), nil},
	} {
		got, err := l.Sessions(ctx, c.s)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, want %v", name, got, err, c.want)
		}
	}

	got, err := l.ScopeEvents(ctx, TaskScope("p", "FT-1"))
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, e := range got {
		ids = append(ids, e.ID)
	}

	if want := []string{events[0].ID, events[2].ID, events[3].ID, events[4].ID}; !slices.Equal(ids, want) {
		t.Errorf("events: %v, want %v", ids, want)
	}

	if got, err := l.ScopeEvents(ctx, TaskScope("p", "FT-404")); err != nil || len(got) != 0 {
		t.Errorf("an unknown task reads %d rows, %v", len(got), err)
	}
}

func mustPayload(kind string) any {
	switch kind {
	case SessionStopped:
		return SessionStoppedPayload{}
	case StateSet:
		return ClaimPayload{}
	}

	return PushedPayload{}
}
