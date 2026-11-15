package api

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/oklog/ulid/v2"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/asknotify"
	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/target"
)

type eventStream = *connect.ServerStreamForClient[f10v1.WatchEventsResponse]

// watcher opens WatchEvents streams on a test server; each closes with t.
func watcher(t *testing.T) func(*f10v1.WatchEventsRequest) eventStream {
	t.Helper()

	srv := httptest.NewServer(Handler(&fakeSup{}))
	t.Cleanup(srv.Close)

	c := f10v1connect.NewEventServiceClient(srv.Client(), srv.URL)

	return func(req *f10v1.WatchEventsRequest) eventStream {
		ctx, cancel := context.WithCancel(t.Context())

		stream, err := c.WatchEvents(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { cancel(); _ = stream.Close() })

		return stream
	}
}

// row records an event of kind for task, its id named so the test can match it.
func row(task, kind string, payload any) ledger.Event {
	e := ledger.MustEvent(kind, payload)
	e.ID, e.Task = ulid.Make().String(), task
	ledger.Record(e)

	return e
}

// next is the stream's next event id, failing on a start marker.
func next(t *testing.T, s eventStream) string {
	t.Helper()

	if !s.Receive() {
		t.Fatalf("stream ended: %v", s.Err())
	}

	ev := s.Msg().GetEvent()
	if ev == nil {
		t.Fatalf("got a start marker, want an event: %v", s.Msg())
	}

	return ev.GetId()
}

// A stream started at the tail says where that is; a client that drops and
// resumes from its last cursor gets every row in between, once and in order,
// a spooled row whose id predates the others included.
func TestWatchEventsStartsAtTheTailAndResumes(t *testing.T) {
	seam.Swap(t, &EventPoll, 20*time.Millisecond)

	watch := watcher(t)
	req := func(after string) *f10v1.WatchEventsRequest {
		return &f10v1.WatchEventsRequest{After: after, Task: "FT-11", Tail: after == ""}
	}

	row("FT-11", ledger.WaitStarted, &ledger.WaitStartedPayload{})

	s := watch(req(""))
	if !s.Receive() || s.Msg().GetEvent() != nil || s.Msg().GetCursor() == "" {
		t.Fatalf("first response = %v, %v; want a start marker with a cursor", s.Msg(), s.Err())
	}

	marker := s.Msg().GetCursor()

	answered := row("FT-11", ledger.AskAnswered, &ledger.AskAnsweredPayload{
		Id: "k", Answer: "yes", AnsweredBy: agentsv1.AnswerSource_ANSWER_SOURCE_PANE,
	})
	if id := next(t, s); id != answered.ID {
		t.Fatalf("first event = %s, want the row recorded after the start, %s", id, answered.ID)
	}

	if got := s.Msg().GetEvent().GetAskAnswered().GetAnsweredBy(); got != agentsv1.AnswerSource_ANSWER_SOURCE_PANE {
		t.Errorf("answered_by = %v", got)
	}

	// minted first, recorded last: a spooled row whose id predates the others
	late := ulid.Make().String()
	missed := []string{row("FT-11", ledger.WaitEnded, &ledger.WaitEndedPayload{}).ID}

	e := ledger.MustEvent(ledger.WaitStarted, &ledger.WaitStartedPayload{})
	e.ID, e.Task = late, "FT-11"
	ledger.Record(e)

	missed = append(missed, late)

	resumed := watch(req(marker))
	for i, want := range append([]string{answered.ID}, missed...) {
		if id := next(t, resumed); id != want {
			t.Fatalf("resumed event %d = %s, want %s", i, id, want)
		}
	}

	if ev, err := EventOf(ledger.Event{ID: "x", Kind: "made_up"}); err != nil || ev.GetPayload() != nil {
		t.Errorf("an unknown kind = %v, %v; want no payload", ev, err)
	}
}

// Kinds narrow the stream, and a kind the ledger does not know is refused.
func TestWatchEventsFiltersByKind(t *testing.T) {
	seam.Swap(t, &EventPoll, 20*time.Millisecond)

	watch := watcher(t)

	s := watch(&f10v1.WatchEventsRequest{
		Task: "FT-2", Tail: true, Kinds: []string{ledger.AskAnswered, ledger.WatchReported},
	})
	if !s.Receive() {
		t.Fatal(s.Err())
	}

	row("FT-2", ledger.WaitStarted, &ledger.WaitStartedPayload{})
	reported := row("FT-2", ledger.WatchReported, &ledger.WatchReportedPayload{Report: "stopped", Text: "ship done"})

	if id := next(t, s); id != reported.ID {
		t.Fatalf("event = %s, want only the watch_reported row %s", id, reported.ID)
	}

	if got := s.Msg().GetEvent().GetWatchReported().GetReport(); got != "stopped" {
		t.Errorf("report = %q, want stopped", got)
	}

	bad := watch(&f10v1.WatchEventsRequest{Kinds: []string{"ask_rasied"}})
	if bad.Receive() || connect.CodeOf(bad.Err()) != connect.CodeInvalidArgument {
		t.Errorf("an unknown kind = %v, %v; want InvalidArgument", bad.Msg(), bad.Err())
	}
}

// Ask rows carry no project, so a project filter matches them by their main
// checkout's project, raised and answered alike.
func TestWatchEventsMatchesAsksToTheirProject(t *testing.T) {
	seam.Swap(t, &EventPoll, 20*time.Millisecond)
	seam.Swap(t, &target.ProjectOf, func(_ context.Context, dir string) string {
		return map[string]string{"/src/a": "github.com/o/a", "/src/b": "github.com/o/b"}[dir]
	})

	dir := t.TempDir()
	watch := watcher(t)

	s := watch(&f10v1.WatchEventsRequest{Project: "github.com/o/a", Tail: true})
	if !s.Receive() {
		t.Fatal(s.Err())
	}

	ask := func(main, task string) asks.Ask {
		a, _, err := asks.Raise(
			dir, asks.Ask{Task: task, Root: main + "/.f10", Main: main, Text: "1. Go? [yes | no]"}, time.Now(),
		)
		if err != nil {
			t.Fatal(err)
		}

		return a
	}

	ask("/src/b", "FT-3")
	mine := ask("/src/a", "FT-3")

	if _, err := asks.Claim(dir, mine.Key(), asks.ByAsks, "yes", false, time.Now()); err != nil {
		t.Fatal(err)
	}

	if !s.Receive() || s.Msg().GetEvent().GetAskRaised().GetAsk().GetLabels()["main"] != "/src/a" {
		t.Fatalf("first event = %v, %v; want project a's raised ask", s.Msg(), s.Err())
	}

	if !s.Receive() || s.Msg().GetEvent().GetAskAnswered().GetId() != mine.Key() {
		t.Fatalf("second event = %v, %v; want project a's answer", s.Msg(), s.Err())
	}
}

// An ask a driven agent raises reaches a stream within a second, at the
// production poll.
func TestWatchEventsDeliversAnAskWithinASecond(t *testing.T) {
	s := watcher(t)(&f10v1.WatchEventsRequest{Task: "FT-4", Tail: true, Kinds: []string{ledger.AskRaised}})
	if !s.Receive() {
		t.Fatal(s.Err())
	}

	got := make(chan error, 1)

	go func() {
		if !s.Receive() {
			got <- s.Err()

			return
		}

		got <- nil
	}()

	start := time.Now()
	asknotify.RaiseAsk(t.Context(), io.Discard, t.TempDir(),
		asks.Ask{Task: "FT-4", Root: "/src/d/.f10", Main: "/src/d", Text: "1. Merge? [yes | no]"}, start, nil)

	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}

		if waited := time.Since(start); waited > time.Second {
			t.Errorf("the ask arrived after %v, want within a second", waited)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(errors.New("the ask never arrived"))
	}
}
