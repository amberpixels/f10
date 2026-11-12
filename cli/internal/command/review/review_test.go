package review_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	reviewcmd "github.com/amberpixels/f10/cli/internal/command/review"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// An older review reacted to after its last edit is handled; a sticky one
// re-edited after the reaction is unhandled again, as a re-review must read.
// Pick returns it with the sha the PR had when it was posted.
func TestReviewPickSkipsHandledAndSeesThroughStaleReactions(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	f.Script(testkit.CommentsPath, `[
	  {"id":100,"body":"round one","html_url":"https://github.com/o/r/pull/5#issuecomment-100",
	   "created_at":"2026-10-06T11:00:00Z","updated_at":"2026-10-06T11:00:00Z","user":{"login":"claude[bot]"}},
	  {"id":200,"body":"round two","html_url":"https://github.com/o/r/pull/5#issuecomment-200",
	   "created_at":"2026-10-06T13:00:00Z","updated_at":"2026-10-06T15:00:00Z","user":{"login":"claude[bot]"}},
	  {"id":300,"body":"a human","html_url":"x","created_at":"2026-10-06T16:00:00Z","updated_at":"2026-10-06T16:00:00Z",
	   "user":{"login":"eugene"}}
	]`)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "100", 1),
		`[{"content":"eyes","created_at":"2026-10-06T11:30:00Z","user":{"login":"eugene"}}]`)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "200", 1),
		`[{"content":"eyes","created_at":"2026-10-06T14:30:00Z","user":{"login":"eugene"}},
		  {"content":"eyes","created_at":"2026-10-06T15:30:00Z","user":{"login":"someone-else"}}]`)

	r, err := rv.Pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	if r.ID != "200" || r.Handled || !r.Done {
		t.Errorf("picked %+v, want the sticky comment 200, done and unhandled", r)
	}

	// posted at 13:00, after c2 (12:00) and before c3 (14:00)
	if r.SHA != "c2" {
		t.Errorf("sha = %q, want c2", r.SHA)
	}

	doc := reviewcmd.ReviewDoc(r)
	for _, want := range []string{"# review by claude[bot] · issue comment", "https://github.com/o/r/pull/5#issuecomment-200", "c2", "round two"} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc missing %q:\n%s", want, doc)
		}
	}
}

// A review that never completes returns within the budget and says so.
func TestAwaitReviewGivesUpWithinBudget(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())
	f.Script(testkit.CommentsPath, `[]`)

	started := time.Now()

	_, err := reviewcmd.AwaitReview(t.Context(), rv, time.Time{}, 40*time.Millisecond, 5*time.Millisecond)
	if err == nil {
		t.Fatal("wait returned a review from an empty thread")
	}

	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("wait took %s on a 40ms budget", elapsed)
	}

	for _, want := range []string{"no completed review from claude[bot] within 40ms", "no review from claude[bot]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	// several polls happened, not one
	polls := 0

	for _, c := range f.Calls {
		if c == testkit.CommentsPath {
			polls++
		}
	}

	if polls < 2 {
		t.Errorf("comments were read %d time(s), want repeated polling", polls)
	}
}

func TestAwaitReviewReturnsOnceDone(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Done, rf.DoneText = facts.DoneMarker, "Review complete"
	rv := testkit.GhReviewer(t, f, rf)

	running := `[{"id":100,"body":"Reviewing...","html_url":"u","created_at":"2026-10-06T11:00:00Z",
	  "updated_at":"2026-10-06T11:00:00Z","user":{"login":"claude[bot]"}}]`
	finished := strings.Replace(running, "Reviewing...", "Findings... Review complete", 1)

	f.Script(testkit.CommentsPath, running, running, finished)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "100", 1), `[]`)

	r, err := reviewcmd.AwaitReview(t.Context(), rv, time.Time{}, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	if !r.Done || !strings.Contains(r.Body, "Review complete") {
		t.Errorf("wait returned %+v before the marker", r)
	}
}

// A settled review counts as handled, so pick passes over it, while
// --latest returns it with both flags set.
func TestReviewSettledReadsAsHandled(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.SettledFacts())

	f.Script(
		testkit.CommentsPath,
		`[{"id":200,"body":"the review","html_url":"u200","created_at":"2026-10-06T13:00:00Z",
	  "updated_at":"2026-10-06T13:00:00Z","user":{"login":"claude[bot]"}}]`,
	)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "200", 1),
		`[{"content":"rocket","created_at":"2026-10-06T14:00:00Z","user":{"login":"eugene"}}]`)

	if r, err := rv.Pick(t.Context()); !errors.Is(err, reviews.ErrNoReview) {
		t.Errorf("pick returned the settled review: %+v, %v", r, err)
	}

	r, err := rv.Latest(t.Context())
	if err != nil || r.ID != "200" || !r.Settled || !r.Handled || r.Revision == "" {
		t.Errorf("latest = %+v, %v; want 200, settled and handled, with a revision", r, err)
	}

	// handled and settled share one reactions read per review
	if n := strings.Count(strings.Join(f.Calls, "\n"), strings.Replace(testkit.ReactionsPath, "%s", "200", 1)); n != 2 {
		t.Errorf("reactions read %d times over two calls, want 2", n)
	}

	if doc := reviewcmd.ReviewDoc(
		r,
	); !strings.Contains(doc, "settled") ||
		!strings.Contains(doc, "revision "+r.Revision) {
		t.Errorf("doc missing the settled state or the revision:\n%s", doc)
	}
}

// After a trigger, an older review that is done and unhandled is not the
// answer: wait holds out for one updated after since.
func TestAwaitReviewSinceSkipsTheOlderReview(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	old := `[{"id":100,"body":"round one","html_url":"u","created_at":"2026-10-06T11:00:00Z",
	  "updated_at":"2026-10-06T11:00:00Z","user":{"login":"claude[bot]"}}]`
	rewritten := strings.Replace(strings.Replace(old, "round one", "round two", 1),
		`"updated_at":"2026-10-06T11:00:00Z"`, `"updated_at":"2026-10-06T16:05:00Z"`, 1)

	f.Script(testkit.CommentsPath, old, old, rewritten)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "100", 1), `[]`)

	since := time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)

	r, err := reviewcmd.AwaitReview(t.Context(), rv, since, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	if r.Body != "round two" {
		t.Errorf("wait returned %q, want the review updated after the trigger", r.Body)
	}

	f.Script(testkit.CommentsPath, old)

	_, err = reviewcmd.AwaitReview(t.Context(), rv, since, 20*time.Millisecond, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "predates 2026-10-06T16:00:00Z") {
		t.Errorf("err = %v", err)
	}
}
