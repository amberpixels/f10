package stats_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	statscmd "github.com/amberpixels/f10/cli/internal/command/stats"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// scopeFixture is StatEvents plus the shapes a scoped read could get wrong:
// a session whose waits come before its task is known, a step spanning the
// cutoff, a task with old sessions and one recent, a size recorded from
// another task's session and one from no session, an unrelated task and
// another project's rows.
func scopeFixture(t *testing.T, day time.Time) (string, time.Time) {
	t.Helper()

	at := func(n int) time.Time { return day.Add(time.Duration(n) * time.Minute) }
	ev := func(n int, session, task, kind string, payload any) ledger.Event {
		return testkit.StatEvent(at(n), session, task, kind, payload)
	}
	set := func(n int, session, task string, args ...string) ledger.Event {
		return ev(n, session, task, ledger.StateSet, ledger.ClaimPayload{Args: args})
	}
	ship := func(n int, session string) ledger.Event {
		return ev(n, session, "", ledger.RunStarted, ledger.RunStartedPayload{Skill: "ship"})
	}
	wait := func(n int, session, task string) ledger.Event {
		return ev(n, session, task, ledger.WaitStarted, ledger.WaitStartedPayload{Reason: "turn"})
	}
	waited := func(n int, session, task string) ledger.Event {
		return ev(n, session, task, ledger.WaitEnded, ledger.WaitEndedPayload{Reason: "turn"})
	}
	const d = 1440

	events := testkit.StatEvents(day)
	events = append(events,
		// FT-3: taskless waits first, then an implement spanning the cutoff (day 30)
		ship(30*d-60, "s3"),
		wait(30*d-58, "s3", ""),
		waited(30*d-50, "s3", ""),
		set(30*d-40, "s3", "FT-3", "ship", "running", "implement"),
		wait(30*d-20, "s3", "FT-3"),
		waited(30*d+10, "s3", "FT-3"),
		set(30*d+30, "s3", "FT-3", "ship", "done"),
		wait(30*d+31, "s3", "FT-3"),
		ev(30*d+32, "s2", "FT-3", ledger.TaskSized, ledger.TaskSizedPayload{Lines: 50}),

		// FT-4: an old session, then a recent one
		ship(10*d, "s4"),
		set(10*d, "s4", "FT-4", "ship", "running", "implement"),
		set(10*d+20, "s4", "FT-4", "ship", "done"),
		wait(10*d+20, "s4", "FT-4"),
		ship(31*d, "s5"),
		set(31*d, "s5", "FT-4", "ship", "running", "pr"),
		set(31*d+5, "s5", "FT-4", "ship", "done"),
		wait(31*d+5, "s5", "FT-4"),
		ev(31*d+6, "", "FT-4", ledger.TaskSized, ledger.TaskSizedPayload{Lines: 80}),

		// FT-9: unrelated, old
		ship(5*d, "s9"),
		set(5*d, "s9", "FT-9", "ship", "running", "implement"),
		set(5*d+50, "s9", "FT-9", "ship", "done"),
		wait(5*d+50, "s9", "FT-9"),
	)

	for i := range events {
		events[i].Project = "p"
	}

	other := ev(31*d, "s3", "FT-3", ledger.StateSet, ledger.ClaimPayload{Args: []string{"ship", "running", "pr"}})
	other.Project = "q"
	events = append(events, other)

	slices.SortStableFunc(events, func(a, b ledger.Event) int { return a.At.Compare(b.At) })

	for i := range events {
		events[i].ID = fmt.Sprintf("01J%023d", i)
	}

	path := filepath.Join(t.TempDir(), "f10.db")

	l, err := ledger.Open(context.Background(), path, ledger.BusyAsync, false)
	if err != nil {
		t.Fatal(err)
	}

	l.Append(context.Background(), events...)
	_ = l.Close()

	return path, at(30 * d)
}

func read(t *testing.T, path string, s ledger.Scope) []ledger.Event {
	t.Helper()

	events, err := ledger.ReadScope(context.Background(), path, s)
	if err != nil {
		t.Fatal(err)
	}

	return events
}

// A scoped read gives the numbers the whole project's read gives.
func TestScopedReadsKeepTheNumbers(t *testing.T) {
	day := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	path, cutoff := scopeFixture(t, day)

	all, err := ledger.ReadEvents(context.Background(), path, ledger.Filter{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}

	for _, task := range []string{"FT-1", "FT-3", "FT-4"} {
		want := statscmd.StatRows(all, task, cutoff)
		if len(want) != 1 {
			t.Fatalf("%s: the fixture shows %d rows", task, len(want))
		}

		got := statscmd.StatRows(read(t, path, ledger.TaskScope("p", task)), task, cutoff)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", task, got, want)
		}
	}

	want := statscmd.StatRows(all, "", cutoff)
	if tasks := len(want); tasks != 2 {
		t.Fatalf("since: the fixture shows %d tasks, want FT-3 and FT-4", tasks)
	}

	got := statscmd.StatRows(read(t, path, ledger.SinceScope("p", cutoff)), "", cutoff)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("since:\n got %+v\nwant %+v", got, want)
	}
}

// A task's read holds only its sessions and its session-less rows.
func TestATaskReadIsBoundedByItsSessions(t *testing.T) {
	path, _ := scopeFixture(t, time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

	for _, e := range read(t, path, ledger.TaskScope("p", "FT-4")) {
		if e.Project != "p" || !slices.Contains([]string{"s4", "s5", ""}, e.Session) {
			t.Errorf("read %s %s %s %s", e.Project, e.Session, e.Task, e.Kind)
		}
	}
}
