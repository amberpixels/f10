package lookaround

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
)

// The feed over one Monday: today's span, Friday the last working day.
var (
	feedNow   = time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	feedToday = Window{
		Since:  time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		Chosen: "today",
	}
	feedFriday = Window{
		Since:  time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC),
		Chosen: "last-workday",
	}
	hourToday   = func(h int) time.Time { return feedToday.Since.Add(time.Duration(h) * time.Hour) }
	hourFriday  = func(h int) time.Time { return feedFriday.Since.Add(time.Duration(h) * time.Hour) }
	longAgo     = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	commitEvent = func(at time.Time, msg string) forge.Event {
		return forge.Event{Kind: forge.EventCommit, At: at, Message: msg}
	}
)

func touched(n int, title, head, status string, created time.Time, events ...forge.Event) forge.Touched {
	return forge.Touched{
		Number: n, Title: title, Head: head, Status: status, CreatedAt: created, Author: "ann", Base: "main",
		URL:    "https://x.io/pull/" + title,
		Events: append(events, forge.Event{Kind: forge.EventOpened, At: created, Actor: "ann"}),
	}
}

// feedFacts is one of every rule: merged, abandoned, a follow-up, a first
// request with no task, a review's verdict, a review step's trailer,
// plain updates, a traced update, a request quiet today, a new task.
func feedFacts() Facts {
	merged := touched(1, "FT-1: merge me", "FT-1/a", forge.Merged, longAgo,
		forge.Event{Kind: forge.EventMerged, At: hourToday(9), Actor: "bob"})
	merged.MergedAt, merged.MergedBy = hourToday(9), "bob"

	closed := touched(2, "FT-2: drop me", "FT-2/a", forge.Closed, longAgo,
		forge.Event{Kind: forge.EventClosed, At: hourToday(10)})
	closed.ClosedAt = hourToday(10)

	reviewed := touched(5, "FT-5: review me", "FT-5/a", forge.Open, longAgo,
		forge.Event{Kind: forge.EventReview, At: hourToday(11), Actor: "bob", State: forge.Approved},
		commitEvent(hourToday(8), "fix"))

	return Facts{
		Prefix: "FT",
		Tasks: []Task{
			{ID: "FT-3", Title: "the task with two requests", URL: "https://x.io/issues/3"},
			{ID: "FT-20", Title: "a new task", Created: hourToday(7), Assignee: "cat"},
			{ID: "FT-21", Title: "an old task", Created: longAgo},
		},
		Activity: &forge.Activity{
			QuietCount:  3,
			QuietOldest: longAgo,
			Requests: []forge.Touched{
				merged,
				closed,
				touched(3, "FT-3: again", "FT-3/b", forge.Open, hourToday(12)),
				touched(4, "no task here", "fix-typo", forge.Open, hourToday(13),
					forge.Event{Kind: forge.EventReviewRequested, At: hourToday(13), Actor: "dan"}),
				reviewed,
				touched(6, "FT-6: resolve", "FT-6/a", forge.Open, longAgo,
					commitEvent(hourToday(9), "fix finding\n\nTask: FT-6\nStep: resolve")),
				touched(7, "FT-7: work", "FT-7/a", forge.Open, longAgo,
					commitEvent(hourToday(8), "one"), commitEvent(hourToday(9), "two"),
					forge.Event{Kind: forge.EventMergeFromBase, At: hourToday(10)},
					forge.Event{Kind: forge.EventForcePush, At: hourToday(11)}),
				touched(8, "FT-8: traced", "FT-8/a", forge.Open, longAgo,
					commitEvent(hourToday(8), "code\n\nTask: FT-8\nStep: implement")),
				touched(9, "FT-9: friday", "FT-9/a", forge.Open, longAgo, commitEvent(hourFriday(9), "friday")),
			},
		},
		Earlier: map[string][]forge.PR{
			"FT-3": {{Number: 30, Title: "FT-3: first", Head: "FT-3/a", Status: forge.Merged, CreatedAt: longAgo}},
		},
	}
}

func TestDeriveFeed(t *testing.T) {
	b := DeriveFeed(feedFacts(), feedNow, feedToday)

	type want struct{ group, from, summary, owes string }

	got := map[string]want{}
	var order []string

	for _, r := range b.Feed.Rows {
		got[r.Key] = want{r.Group, r.From, r.Summary, r.Owes}
		order = append(order, r.Key)
	}

	for key, w := range map[string]want{
		"FT-1":  {GroupFinished, FromHost, "merged by bob", ""},
		"FT-2":  {GroupFinished, FromHost, "abandoned", ""},
		"FT-3":  {GroupStarted, FromHost, "second request on FT-3", ""},
		"#4":    {GroupStarted, FromHost, "opened", "dan"},
		"FT-5":  {GroupReviewed, FromGuessed, "approved by bob", "ann"},
		"FT-6":  {GroupReviewed, FromTrace, "resolve pushed", ""},
		"FT-7":  {GroupUpdated, FromGuessed, "2 commits, merged main, 1 force push", ""},
		"FT-8":  {GroupUpdated, FromTrace, "implement: 1 commit", ""},
		"FT-20": {GroupNewTasks, FromTracker, "created", ""},
	} {
		if got[key] != w {
			t.Errorf("%s = %+v, want %+v", key, got[key], w)
		}
	}

	if _, ok := got["FT-9"]; ok {
		t.Error("FT-9 had activity on Friday only, yet is a row today")
	}

	if _, ok := got["FT-21"]; ok {
		t.Error("an old task is a new task")
	}

	if q := b.Feed.Quiet; q.Count != 4 || !q.Oldest.Equal(longAgo) {
		t.Errorf("quiet = %+v, want the three untouched and FT-9", q)
	}

	// groups in order, the latest first inside one
	if want := []string{
		"#4",
		"FT-3",
		"FT-7",
		"FT-8",
		"FT-5",
		"FT-6",
		"FT-2",
		"FT-1",
		"FT-20",
	}; !slices.Equal(
		order,
		want,
	) {
		t.Errorf("order = %v, want %v", order, want)
	}

	if r := b.Feed.Rows[slices.Index(order, "FT-3")]; !slices.Equal(r.FollowUpOf, []int{30}) || r.Task == nil {
		t.Errorf("FT-3 = %+v, want a follow-up of #30 joined to its task", r)
	}

	if len(b.Rows) != 0 || b.View != ViewFeed {
		t.Errorf("view %q with %d state rows", b.View, len(b.Rows))
	}

	golden(t, "testdata/feed/monday.board.json", b)
}

// A morning with nothing yet today falls back to the last working day;
// one thing today keeps today.
func TestChooseWindow(t *testing.T) {
	quiet := Facts{Activity: &forge.Activity{Requests: []forge.Touched{
		touched(9, "FT-9", "FT-9/a", forge.Open, longAgo, commitEvent(hourFriday(9), "friday")),
	}}}

	w := ChooseWindow(feedToday, feedFriday, quiet)
	if w.Chosen != "last-workday" || !w.Fallback {
		t.Errorf("quiet morning = %+v, want the last workday as the fallback", w)
	}

	newTask := Facts{Tasks: []Task{{ID: "FT-1", Created: hourToday(1)}}}
	if w := ChooseWindow(feedToday, feedFriday, quiet, newTask); w.Chosen != "today" || w.Fallback {
		t.Errorf("active day = %+v, want today", w)
	}

	b := DeriveFeed(quiet, feedNow, ChooseWindow(feedToday, feedFriday, quiet))
	if len(b.Feed.Rows) != 1 || b.Feed.Rows[0].Group != GroupUpdated {
		t.Errorf("Friday's feed = %+v", b.Feed.Rows)
	}
}

// A local-only checkout's feed is its branches committed in the window.
func TestDeriveFeedTips(t *testing.T) {
	f := Facts{Prefix: "FT", Tips: []Tip{{Branch: "FT-4/x", At: hourToday(2)}, {Branch: "old", At: longAgo}}}

	b := DeriveFeed(f, feedNow, feedToday)
	if len(b.Feed.Rows) != 1 || b.Feed.Rows[0].Key != "FT-4" || b.Feed.Rows[0].From != FromLocal {
		t.Errorf("rows = %+v", b.Feed.Rows)
	}
}

func golden(t *testing.T, path string, v any) {
	t.Helper()

	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	got = append(got, '\n')

	if os.Getenv("F10_UPDATE") != "" {
		if err := os.MkdirAll("testdata/feed", 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (F10_UPDATE=1 writes it)", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("differs from %s:\n%s", path, got)
	}
}
