package reviews_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A line naming both kinds hints with its manual part alone.
func TestManualHintNamesTheManualPart(t *testing.T) {
	rf := testkit.StickyFacts()
	rf.Trigger = facts.TriggerAutomatic + ", " + facts.TriggerManual
	rf.TriggerHow = "on open and on push; manual, comment `@claude` on the PR for a re-review"
	rf.ManualHow = "comment `@claude` on the PR for a re-review"

	got := (&reviews.Reviewer{Facts: rf}).ManualHint()
	if want := "; a re-review is manual (comment `@claude` on the PR for a re-review) and " +
		"`f10 review trigger` performs it"; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
}

func TestReviewPickNamesTheManualTriggerWhenNothingIsUnhandled(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Trigger, rf.TriggerHow = facts.TriggerManual, "comment `@claude` on the PR"
	rv := testkit.GhReviewer(t, f, rf)

	f.Script(testkit.CommentsPath, `[{"id":100,"body":"done","html_url":"u","created_at":"2026-10-06T11:00:00Z",
	  "updated_at":"2026-10-06T11:00:00Z","user":{"login":"claude[bot]"}}]`)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "100", 1),
		`[{"content":"eyes","created_at":"2026-10-06T11:30:00Z","user":{"login":"eugene"}}]`)

	_, err := rv.Pick(t.Context())
	if !errors.Is(err, reviews.ErrNoReview) {
		t.Fatalf("err = %v, want errNoReview", err)
	}

	for _, want := range []string{"the trigger is manual", "@claude", "`f10 review trigger` performs it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	// nothing posted at all is the other refusal, with the same hint
	f.Script(testkit.CommentsPath, `[]`)

	_, err = rv.Pick(t.Context())
	if !errors.Is(err, reviews.ErrNoReview) || !strings.Contains(err.Error(), "the trigger is manual") {
		t.Errorf("err = %v", err)
	}
}

func TestReviewAckReactsOnceAndSkipsCheckRuns(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	unhandled := reviews.RemoteReview{ID: "200", Kind: facts.ArrivesComment, URL: "u", Host: forge.Note{ID: "200"}}
	post := "gh api -X POST repos/{owner}/{repo}/issues/comments/200/reactions -f content=eyes"
	f.Script(post, `{"id":1}`)

	what, err := rv.Ack(t.Context(), unhandled)
	if err != nil || what != "reaction eyes" {
		t.Errorf("ack = %q, %v", what, err)
	}

	if !f.Called(post) {
		t.Errorf("no reaction posted: %v", f.Calls)
	}

	before := len(f.Calls)

	if what, err := rv.Ack(
		t.Context(),
		reviews.RemoteReview{ID: "200", Handled: true},
	); err != nil ||
		what != "already handled" {
		t.Errorf("ack on handled = %q, %v", what, err)
	}

	what, err = rv.Ack(t.Context(), reviews.RemoteReview{ID: "9", Kind: facts.ArrivesCheck})
	if err != nil || !strings.Contains(what, "no handled marker") {
		t.Errorf("ack on a check run = %q, %v", what, err)
	}

	if len(f.Calls) != before {
		t.Errorf("a confirmed or check-run ack reached the host: %v", f.Calls[before:])
	}
}

func TestReviewAckTicksTheCheckboxAndAppendsTheMarker(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Handled, rf.HandledText = facts.HandledCheckbox, ""
	rv := testkit.GhReviewer(t, f, rf)

	patch := "gh api -X PATCH repos/{owner}/{repo}/issues/comments/7 -f body=findings\n\n- [x] handled"
	f.Script(patch, `{}`)

	if _, err := rv.Ack(
		t.Context(),
		reviews.RemoteReview{
			ID:   "7",
			Kind: facts.ArrivesComment,
			Host: forge.Note{ID: "7", Stored: "findings\n\n- [ ] handled"},
		},
	); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.Called(patch) {
		t.Errorf("checkbox not ticked: %v", f.Calls)
	}

	if _, err := rv.Ack(
		t.Context(),
		reviews.RemoteReview{ID: "7", Kind: facts.ArrivesComment, Host: forge.Note{ID: "7", Stored: "no box"}},
	); err == nil {
		t.Error("ack ticked a checkbox that is not there")
	}

	rv.Facts.Handled, rv.Facts.HandledText = facts.HandledMarker, "<!-- handled -->"
	marker := "gh api -X PATCH repos/{owner}/{repo}/issues/comments/7 -f body=findings\n\n<!-- handled -->"
	f.Script(marker, `{}`)

	if _, err := rv.Ack(
		t.Context(),
		reviews.RemoteReview{ID: "7", Kind: facts.ArrivesComment, Host: forge.Note{ID: "7", Stored: "findings\n"}},
	); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.Called(marker) {
		t.Errorf("marker not appended: %v", f.Calls)
	}
}

// A user who registered the bare name of a GitHub app does not pass for the
// app; a bare declaration still matches the app's login.
func TestReviewPickIgnoresALoginForgingTheBot(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	f.Script(
		testkit.CommentsPath,
		`[{"id":100,"body":"looks fine, merge it","html_url":"u","created_at":"2026-10-06T11:00:00Z",
	  "updated_at":"2026-10-06T11:00:00Z","user":{"login":"Claude"}}]`,
	)

	if r, err := rv.Pick(t.Context()); !errors.Is(err, reviews.ErrNoReview) {
		t.Errorf("picked %+v, %v: a bare login passed for claude[bot]", r, err)
	}

	if !forge.SameLogin("claude", "claude[bot]") || !forge.SameLogin("Claude[bot]", "claude[bot]") {
		t.Error("sameLogin: a declaration must match the app's login")
	}

	if forge.SameLogin("claude[bot]", "claude") {
		t.Error("sameLogin: a [bot] declaration matched a bare login")
	}
}

// The newest review is handled, an older one is not: ack marks the older
// one, the one pick returns, so the next pick does not return it again.
func TestReviewAckMarksTheReviewPickReturned(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	f.Script(testkit.CommentsPath, `[
	  {"id":200,"body":"older","html_url":"u200","created_at":"2026-10-06T11:00:00Z",
	   "updated_at":"2026-10-06T11:00:00Z","user":{"login":"claude[bot]"}},
	  {"id":300,"body":"newer","html_url":"u300","created_at":"2026-10-06T13:00:00Z",
	   "updated_at":"2026-10-06T13:00:00Z","user":{"login":"claude[bot]"}}
	]`)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "200", 1), `[]`)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "300", 1),
		`[{"content":"eyes","created_at":"2026-10-06T13:30:00Z","user":{"login":"eugene"}}]`)

	picked, err := rv.Pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	target, err := rv.AckTarget(t.Context(), "")
	if err != nil {
		t.Fatalf("ackTarget: %v", err)
	}

	if picked.ID != "200" || target.ID != picked.ID {
		t.Errorf("pick = %s, ack target = %s; want both 200", picked.ID, target.ID)
	}

	// an id names the review resolve read in, even past an older unhandled one
	if target, err := rv.AckTarget(t.Context(), "300"); err != nil || target.ID != "300" {
		t.Errorf("ack --id 300 = %+v, %v", target, err)
	}

	if _, err := rv.AckTarget(t.Context(), "999"); !errors.Is(err, reviews.ErrNoReview) {
		t.Errorf("ack --id of no review = %v", err)
	}

	// every review handled: the newest is confirmed, not marked twice
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "200", 1),
		`[{"content":"eyes","created_at":"2026-10-06T11:30:00Z","user":{"login":"eugene"}}]`)

	if target, err := rv.AckTarget(t.Context(), ""); err != nil || target.ID != "300" || !target.Handled {
		t.Errorf("all handled: ack target = %+v, %v; want 300, handled", target, err)
	}
}

// Only the first box is ticked: the rest may be the reviewer's own list.
func TestReviewAckTicksOnlyTheFirstCheckbox(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Handled, rf.HandledText = facts.HandledCheckbox, ""
	rv := testkit.GhReviewer(t, f, rf)

	patch := "gh api -X PATCH repos/{owner}/{repo}/issues/comments/7 -f body=- [x] handled\n- [ ] fix a.go\n- [ ] fix b.go"
	f.Script(patch, `{}`)

	if _, err := rv.Ack(t.Context(), reviews.RemoteReview{
		ID: "7", Kind: facts.ArrivesComment,
		Host: forge.Note{ID: "7", Stored: "- [ ] handled\n- [ ] fix a.go\n- [ ] fix b.go"},
	}); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.Called(patch) {
		t.Errorf("want only the first box ticked: %v", f.Calls)
	}
}

// A gh inline review: its comments are folded into the body for the reader,
// the marker lands on the review's own summary alone, and a thread ack
// resolves every thread the review opened.
func TestReviewInlineFoldsCommentsAndAcksTheSummaryOnly(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Arrives, rf.Done = facts.ArrivesInline, facts.DoneSubmitted
	rf.Handled, rf.HandledText = facts.HandledMarker, "<!-- handled -->"
	rv := testkit.GhReviewer(t, f, rf)

	f.Script("gh api --paginate repos/{owner}/{repo}/pulls/5/reviews?per_page=100", `[
	  {"id":11,"body":"two things","state":"COMMENTED","commit_id":"c2","html_url":"https://github.com/o/r/pull/5#pullrequestreview-11",
	   "submitted_at":"2026-10-06T13:00:00Z","user":{"login":"claude[bot]"}},
	  {"id":12,"body":"lgtm","state":"APPROVED","commit_id":"c3","html_url":"x",
	   "submitted_at":"2026-10-06T15:00:00Z","user":{"login":"eugene"}}
	]`)
	f.Script("gh api --paginate repos/{owner}/{repo}/pulls/5/reviews/11/comments?per_page=100", `[
	  {"id":901,"path":"a.go","line":3,"body":"nil map","updated_at":"2026-10-06T13:00:00Z"},
	  {"id":902,"path":"b.go","line":9,"body":"leak","updated_at":"2026-10-06T13:05:00Z"}
	]`)
	f.Script("gh repo view --json owner,name", `{"name":"r","owner":{"login":"o"}}`)
	f.Script("gh api graphql -f query="+reviewThreadsQuery+" -F owner=o -F name=r -F number=5",
		`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
		  {"id":"T1","isResolved":false,"comments":{"nodes":[{"pullRequestReview":{"databaseId":11}}]}},
		  {"id":"T2","isResolved":true,"comments":{"nodes":[{"pullRequestReview":{"databaseId":11}}]}},
		  {"id":"T9","isResolved":false,"comments":{"nodes":[{"pullRequestReview":{"databaseId":12}}]}}
		]}}}}}`)

	r, err := rv.Pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	if r.ID != "11" || r.SHA != "c2" || !r.Done || r.Handled {
		t.Errorf("picked %+v", r)
	}

	for _, want := range []string{"two things", "### a.go:3\n\nnil map", "### b.go:9\n\nleak"} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("body missing %q:\n%s", want, r.Body)
		}
	}

	put := "gh api -X PUT repos/{owner}/{repo}/pulls/5/reviews/11 -f body=two things\n\n<!-- handled -->"
	f.Script(put, `{}`)

	if _, err := rv.Ack(t.Context(), r); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.Called(put) {
		t.Errorf("the marker must land on the summary alone: %v", f.Calls)
	}

	rv.Facts.Handled = facts.HandledThread
	for _, id := range []string{"T1", "T2"} {
		f.Script("gh api graphql -f query="+resolveThreadMutation+" -F id="+id, `{}`)
	}

	if what, err := rv.Ack(t.Context(), r); err != nil || what != "2 thread(s) resolved" {
		t.Errorf("thread ack = %q, %v", what, err)
	}
}

// A thread lookup that fails still picks the review, and the thread ack
// reports the lookup's failure instead of claiming there are no threads.
func TestReviewInlineThreadLookupFailureReachesTheAck(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Arrives, rf.Done, rf.Handled = facts.ArrivesInline, facts.DoneSubmitted, facts.HandledThread
	rv := testkit.GhReviewer(t, f, rf)

	f.Script("gh api --paginate repos/{owner}/{repo}/pulls/5/reviews?per_page=100", `[
	  {"id":11,"body":"two things","state":"COMMENTED","commit_id":"c2","html_url":"x",
	   "submitted_at":"2026-10-06T13:00:00Z","user":{"login":"claude[bot]"}}
	]`)
	f.Script("gh api --paginate repos/{owner}/{repo}/pulls/5/reviews/11/comments?per_page=100", `[
	  {"id":901,"path":"a.go","line":3,"body":"nil map","updated_at":"2026-10-06T13:00:00Z"}
	]`)
	f.Script("gh repo view --json owner,name", `{"name":"r","owner":{"login":"o"}}`)
	f.Fail("gh api graphql -f query="+reviewThreadsQuery+" -F owner=o -F name=r -F number=5",
		shell.Result{Code: 1, Stderr: "GraphQL: rate limited"})

	r, err := rv.Pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	_, err = rv.Ack(t.Context(), r)
	if err == nil || !strings.Contains(err.Error(), "reading the review's threads") ||
		!strings.Contains(err.Error(), "rate limited") {
		t.Errorf("thread ack error = %v", err)
	}
}

// A check run is done when it concluded, and its body is its output.
func TestReviewCheckRun(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Reviewer, rf.ReviewerKind, rf.Arrives = "claude-review", facts.ReviewerCheck, facts.ArrivesCheck
	rf.Done, rf.Handled, rf.HandledText = facts.DoneConcluded, "", ""
	rv := testkit.GhReviewer(t, f, rf)

	f.Script("gh api repos/{owner}/{repo}/commits/c3/check-runs?per_page=100", `{"check_runs":[
	  {"id":5,"name":"lint","status":"completed","html_url":"x","head_sha":"c3"},
	  {"id":6,"name":"Claude-Review","status":"in_progress","html_url":"https://github.com/o/r/runs/6","head_sha":"c3",
	   "started_at":"2026-10-06T14:00:00Z","output":{"title":"Review","summary":"one finding","text":"a.go:3 nil map"}}
	]}`)

	r, err := rv.Pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	if r.ID != "6" || r.Done || r.SHA != "c3" || r.Body != "Review\n\none finding\n\na.go:3 nil map" {
		t.Errorf("picked %+v", r)
	}
}

// settle reacts once, confirms a settled review, and leaves nothing on a
// check run; with no settled mark declared it refuses, naming the fact.
func TestReviewSettleReactsOnceAndSkipsCheckRuns(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.SettledFacts())

	post := "gh api -X POST repos/{owner}/{repo}/issues/comments/200/reactions -f content=rocket"
	f.Script(post, `{"id":1}`)

	what, err := rv.Settle(
		t.Context(),
		reviews.RemoteReview{
			ID:      "200",
			Kind:    facts.ArrivesComment,
			URL:     "u",
			Handled: true,
			Host:    forge.Note{ID: "200"},
		},
	)
	if err != nil || what != "reaction rocket" || !f.Called(post) {
		t.Errorf("settle = %q, %v; calls %v", what, err, f.Calls)
	}

	before := len(f.Calls)

	if what, err := rv.Settle(
		t.Context(),
		reviews.RemoteReview{ID: "200", Settled: true},
	); err != nil ||
		what != "already settled" {
		t.Errorf("settle on settled = %q, %v", what, err)
	}

	if what, err := rv.Settle(t.Context(), reviews.RemoteReview{ID: "9", Kind: facts.ArrivesCheck}); err != nil ||
		!strings.Contains(what, "no settled marker") {
		t.Errorf("settle on a check run = %q, %v", what, err)
	}

	if len(f.Calls) != before {
		t.Errorf("a confirmed or check-run settle reached the host: %v", f.Calls[before:])
	}

	none := testkit.GhReviewer(t, f, testkit.StickyFacts())
	if _, err := none.Settle(t.Context(), reviews.RemoteReview{ID: "200", Kind: facts.ArrivesComment}); err == nil ||
		!strings.Contains(err.Error(), "declare `settled`") {
		t.Errorf("settle with none declared = %v", err)
	}
}

// A sticky comment rewritten by a re-review keeps its id and sha; its
// revision moves, and settle refuses it. The marks f10 leaves on the text
// do not move the revision.
func TestReviewSettleRefusesARewrittenReview(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Handled, rf.HandledText = facts.HandledMarker, "<!-- read -->"
	rf.Settled, rf.SettledText = facts.HandledMarker, "<!-- settled -->"
	rv := testkit.GhReviewer(t, f, rf)

	comment := func(body string) string {
		return `[{"id":200,"body":"` + body + `","html_url":"u200","created_at":"2026-10-06T13:00:00Z",
		  "updated_at":"2026-10-06T15:00:00Z","user":{"login":"claude[bot]"}}]`
	}

	f.Script(testkit.CommentsPath, comment("round one"))

	read, err := rv.Latest(t.Context())
	if err != nil {
		t.Fatalf("latest: %v", err)
	}

	f.Script(testkit.CommentsPath, comment("round one\\n\\n<!-- read -->"))

	if r, err := rv.SettleTarget(t.Context(), "200", read.Revision); err != nil || r.ID != "200" {
		t.Errorf("the handled marker moved the revision: %+v, %v", r, err)
	}

	f.Script(testkit.CommentsPath, comment("round two"))

	if _, err := rv.SettleTarget(t.Context(), "200", read.Revision); !errors.Is(err, reviews.ErrReviewChanged) {
		t.Errorf("settled a rewritten review: %v", err)
	}

	if _, err := rv.SettleTarget(t.Context(), "999", read.Revision); !errors.Is(err, reviews.ErrNoReview) {
		t.Errorf("settled a review that is not there: %v", err)
	}
}

// On an inline review the handled marker lands mid-text, before the folded
// comments, after a blank line; the revision reads past both.
func TestReviewRevisionIgnoresTheMarkerOnAnInlineReview(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Arrives = facts.ArrivesInline
	rf.Handled, rf.HandledText = facts.HandledMarker, "<!-- read -->"
	rv := testkit.GhReviewer(t, f, rf)

	comments := "\n\n### a.go:3\n\nnil check"
	read := rv.Revision(reviews.RemoteReview{Body: "summary" + comments})
	acked := rv.Revision(reviews.RemoteReview{Body: "summary\n\n<!-- read -->" + comments})
	rewritten := rv.Revision(reviews.RemoteReview{Body: "summary" + comments + "\n\n### b.go:9\n\nleak"})

	if read != acked {
		t.Errorf("the marker moved the revision: %s, then %s", read, acked)
	}

	if read == rewritten {
		t.Error("a new comment left the revision where it was")
	}
}

// A marker settled mark is appended once and read back as settled, which
// also reads as handled.
func TestReviewSettleAppendsTheMarker(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Settled, rf.SettledText = facts.HandledMarker, "<!-- settled -->"
	rv := testkit.GhReviewer(t, f, rf)

	patch := "gh api -X PATCH repos/{owner}/{repo}/issues/comments/7 -f body=findings\n\n<!-- settled -->"
	f.Script(patch, `{}`)

	what, err := rv.Settle(
		t.Context(),
		reviews.RemoteReview{ID: "7", Kind: facts.ArrivesComment, Host: forge.Note{ID: "7", Stored: "findings\n"}},
	)
	if err != nil || what != "marker appended" || !f.Called(patch) {
		t.Errorf("settle = %q, %v; calls %v", what, err, f.Calls)
	}

	f.Script(testkit.CommentsPath, `[{"id":7,"body":"findings\\n\\n<!-- settled -->","html_url":"u7",
	  "created_at":"2026-10-06T13:00:00Z","updated_at":"2026-10-06T14:00:00Z","user":{"login":"claude[bot]"}}]`)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "7", 1), `[]`)

	r, err := rv.Latest(t.Context())
	if err != nil || !r.Settled || !r.Handled {
		t.Errorf("latest = %+v, %v; want settled and handled through the marker", r, err)
	}
}

// A comment trigger posts the how's code span through the API and prints
// the host's time for it; a review request adds the login.
func TestReviewTriggerCommentsOrRequests(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Trigger, rf.TriggerHow = facts.TriggerManual, "comment `@claude review` on the PR"
	rv := testkit.GhReviewer(t, f, rf)

	f.Script("gh api -X POST repos/{owner}/{repo}/issues/5/comments -f body=@claude review",
		`{"id":900,"html_url":"https://github.com/o/r/pull/5#issuecomment-900","created_at":"2026-10-06T16:00:00Z"}`)

	what, url, at, err := rv.Trigger(t.Context())
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}

	if what != "commented @claude review" || !strings.HasSuffix(url, "#issuecomment-900") ||
		!at.Equal(time.Date(2026, 10, 6, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("trigger = %q, %q, %s", what, url, at)
	}

	rv.Facts.TriggerHow = "request a review from `octocat`"
	f.Script("gh api -X POST repos/{owner}/{repo}/pulls/5/requested_reviewers -f reviewers[]=octocat", `{}`)

	if what, _, _, err = rv.Trigger(t.Context()); err != nil || what != "requested a review from octocat" {
		t.Errorf("request trigger = %q, %v", what, err)
	}
}

// Automatic only, or a how that reads as neither shape, is refused naming
// the Trigger line, and nothing reaches the host.
func TestReviewTriggerRefuses(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	if _, _, _, err := rv.Trigger(t.Context()); err == nil || !strings.Contains(err.Error(), "automatic only") {
		t.Errorf("automatic trigger: err = %v", err)
	}

	rv.Facts.Trigger, rv.Facts.TriggerHow = facts.TriggerManual, "ask in Slack"

	if _, _, _, err := rv.Trigger(t.Context()); err == nil || !strings.Contains(err.Error(), "Trigger how") {
		t.Errorf("unreadable how: err = %v", err)
	}

	if f.Called("gh api -X POST") {
		t.Error("a refused trigger reached the host")
	}
}

// The GraphQL documents forge sends for review threads, as gh's argv carries
// them; a change in forge shows here as an unscripted call.
const (
	reviewThreadsQuery = "query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) " +
		"{ pullRequest(number: $number) { reviewThreads(first: 100) { nodes { id isResolved " +
		"comments(first: 1) { nodes { pullRequestReview { databaseId } } } } } } } }"

	resolveThreadMutation = "mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { isResolved } } }"
)
