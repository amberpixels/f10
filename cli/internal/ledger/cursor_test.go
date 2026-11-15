package ledger

import (
	"testing"

	"github.com/oklog/ulid/v2"
)

// A tail of a database not written yet ends at "0" and reads nothing; once
// rows are spooled, End moves and After reads them by kind, and a row with
// no project reaches a project's tail only through Orphan.
func TestTailEndKindsAndOrphans(t *testing.T) {
	path := tempPath(t)

	tail := OpenTail(path)
	t.Cleanup(func() { _ = tail.Close() })

	if end, err := tail.End(t.Context()); err != nil || end != "0" {
		t.Fatalf("End of no database = %q, %v; want 0", end, err)
	}

	in := func(project, kind string, payload any) Event {
		// Record fills ids on its own copy, so the test names them
		e := MustEvent(kind, payload)
		e.ID, e.Project = ulid.Make().String(), project
		Record(e)

		return e
	}

	in("p", WaitStarted, &WaitStartedPayload{})
	reported := in("p", WatchReported, &WatchReportedPayload{Report: "merged"})
	in("q", WatchReported, &WatchReportedPayload{Report: "merged"})
	asked := in("", AskAnswered, &AskAnsweredPayload{Id: "k"})

	end, err := tail.End(t.Context())
	if err != nil || end == "0" {
		t.Fatalf("End after four rows = %q, %v", end, err)
	}

	page, err := tail.After(t.Context(), "0", Narrow{Project: "p", Kinds: []string{WatchReported, AskAnswered}})
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Rows) != 1 || page.Rows[0].ID != reported.ID || page.Next != end || page.Full {
		t.Fatalf("without Orphan = %+v (end %s), want only p's report and the cursor past the ask", page, end)
	}

	page, err = tail.After(t.Context(), "0", Narrow{
		Project: "p", Kinds: []string{AskAnswered}, Orphan: func(e Event) bool { return e.ID == asked.ID },
	})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ID != asked.ID {
		t.Fatalf("with Orphan = %+v, %v; want the ask", page, err)
	}

	if page, err := tail.After(t.Context(), end, Narrow{}); err != nil || len(page.Rows) != 0 || page.Next != end {
		t.Errorf("after the end = %+v, %v; want nothing and the same cursor", page, err)
	}

	if _, err := tail.After(t.Context(), "x", Narrow{}); err == nil {
		t.Error("a cursor this ledger never gave was read")
	}
}
