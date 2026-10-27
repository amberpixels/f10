package lookaround

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		"FT-3":  {GroupStarted, FromHost, "opened, follow-up to #30", ""},
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

	if r := b.Feed.Rows[slices.Index(order, "FT-3")]; len(r.Related) != 1 || r.Related[0].Number != 30 ||
		r.Related[0].Relation != RelationFollowUp || r.Task == nil {
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

// Every earlier request of a task is listed by how it relates to the
// latest: merged before it opened, still open beside it, closed unmerged.
func TestDeriveFeedRelated(t *testing.T) {
	f := Facts{
		Prefix: "FT",
		Activity: &forge.Activity{Requests: []forge.Touched{
			touched(9, "FT-3: fourth try", "FT-3/d", forge.Open, hourToday(12)),
		}},
		Earlier: map[string][]forge.PR{"FT-3": {
			{Number: 1, Head: "FT-3/a", Status: forge.Merged, CreatedAt: longAgo, MergedAt: longAgo.Add(time.Hour)},
			{Number: 2, Head: "FT-3/b", Status: forge.Open, CreatedAt: longAgo},
			{Number: 3, Head: "FT-3/c", Status: forge.Merged, CreatedAt: longAgo, MergedAt: longAgo.Add(time.Hour)},
			{Number: 4, Head: "FT-3/x", Status: forge.Closed, CreatedAt: longAgo},
		}},
	}

	r := DeriveFeed(f, feedNow, feedToday).Feed.Rows[0]

	var related []string
	for _, p := range r.Related {
		related = append(related, fmt.Sprintf("#%d %s", p.Number, p.Relation))
	}

	if want := []string{"#1 follow-up", "#2 part", "#3 follow-up", "#4 replaced"}; !slices.Equal(related, want) {
		t.Errorf("related = %v, want %v", related, want)
	}

	if r.Summary != "opened (replaced #4), follow-up to #3" {
		t.Errorf("summary = %q", r.Summary)
	}
}

// A task's requests make one row however they relate: two overlapping
// and both merged, one replacing another closed in the window, one
// following up a request merged long ago. Only a request closed with
// nothing after it keeps abandoned.
func TestDeriveFeedOneRowPerTask(t *testing.T) {
	merged := func(n int, head string, created, at time.Time) forge.Touched {
		r := touched(n, "FT-0: part", head, forge.Merged, created, forge.Event{Kind: forge.EventMerged, At: at})
		r.MergedAt = at

		return r
	}

	closed := touched(50, "FT-50: first", "FT-50/a", forge.Closed, longAgo,
		forge.Event{Kind: forge.EventClosed, At: hourToday(9)})
	closed.ClosedAt = hourToday(9)

	f := Facts{
		Prefix: "FT",
		Activity: &forge.Activity{Requests: []forge.Touched{
			merged(40, "FT-40/a", hourToday(1), hourToday(10)),
			merged(41, "FT-40/b", hourToday(2), hourToday(11)),
			closed,
			merged(51, "FT-50/b", hourToday(9), hourToday(12)),
			merged(61, "FT-60/b", longAgo.Add(48*time.Hour), hourToday(10)),
		}},
		Earlier: map[string][]forge.PR{"FT-60": {
			{Number: 60, Head: "FT-60/a", Status: forge.Merged, CreatedAt: longAgo, MergedAt: longAgo.Add(time.Hour)},
		}},
	}

	got := map[string]FeedRow{}
	for _, r := range DeriveFeed(f, feedNow, feedToday).Feed.Rows {
		if _, dup := got[r.Key]; dup {
			t.Errorf("%s has more than one row", r.Key)
		}

		got[r.Key] = r
	}

	for key, want := range map[string]struct {
		pr      int
		summary string
	}{
		"FT-40": {41, "merged in 2 PRs"},
		"FT-50": {51, "merged (replaced #50)"},
		"FT-60": {61, "merged, follow-up to #60"},
	} {
		r := got[key]
		if r.PR == nil || r.PR.Number != want.pr || r.Summary != want.summary || r.Abandoned ||
			r.Group != GroupFinished {
			t.Errorf("%s = %+v, want #%d %q", key, r, want.pr, want.summary)
		}
	}

	if len(got) != 3 {
		t.Errorf("rows = %v, want one per task", got)
	}
}

// A review comment this morning makes no row, so the default still falls
// back to the last working day.
func TestChooseWindowIgnoresRowlessActivity(t *testing.T) {
	f := Facts{Activity: &forge.Activity{Requests: []forge.Touched{
		touched(9, "FT-9", "FT-9/a", forge.Open, longAgo,
			commitEvent(hourFriday(9), "friday"),
			forge.Event{Kind: forge.EventReview, At: hourToday(8), Actor: "bob", State: forge.Commented}),
	}}}

	if w := ChooseWindow(feedToday, feedFriday, f); !w.Fallback {
		t.Errorf("window = %+v, want the fallback", w)
	}
}
