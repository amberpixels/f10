package launch_test

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A review past the first page is the newest one, and pick finds it
// whether the CLI merged the pages or printed one array per page.
func TestReviewPickReadsEveryPage(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	f.Script(testkit.CommentsPath, `[{"id":1,"body":"chatter","html_url":"u","created_at":"2026-10-06T10:00:00Z",
	  "updated_at":"2026-10-06T10:00:00Z","user":{"login":"eugene"}}]
	[{"id":200,"body":"the review","html_url":"u","created_at":"2026-10-06T13:00:00Z",
	  "updated_at":"2026-10-06T13:00:00Z","user":{"login":"claude[bot]"}}]`)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "200", 1), `[]`)

	r, err := rv.Pick(t.Context())
	if err != nil || r.ID != "200" {
		t.Errorf("picked %+v, %v; want the review on page two", r, err)
	}
}

func glabReviewer(t *testing.T, f *testkit.Fakes, rf facts.Review) *reviews.Reviewer {
	t.Helper()

	f.Script(
		"glab mr view -F json",
		`{"iid":7,"web_url":"https://gitlab.com/g/p/-/merge_requests/7","sha":"d2","head_pipeline":{"id":77,"sha":"d2"}}`,
	)
	f.Script("glab api --paginate projects/:fullpath/merge_requests/7/commits?per_page=100",
		`[{"id":"d1","committed_date":"2026-10-06T10:00:00Z"},{"id":"d2","committed_date":"2026-10-06T12:00:00Z"}]`)

	rv, err := reviews.NewReviewer(t.Context(), hostops.NewHost("glab", "."), rf, "")
	if err != nil {
		t.Fatalf("newReviewer: %v", err)
	}

	return rv
}

// GitLab's inline review is the reviewer's open discussions folded into
// one; the marker lands on the first note's own text, not the fold, and a
// thread ack resolves each discussion.
func TestReviewGlabDiscussions(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Arrives, rf.Done = facts.ArrivesInline, facts.DonePosted
	rf.Handled, rf.HandledText = facts.HandledMarker, "<!-- handled -->"
	rv := glabReviewer(t, f, rf)

	f.Script("glab api --paginate projects/:fullpath/merge_requests/7/discussions?per_page=100", `[
	  {"id":"da","notes":[{"id":51,"body":"nil map","resolvable":true,"author":{"username":"claude"},
	    "created_at":"2026-10-06T13:00:00Z","updated_at":"2026-10-06T13:00:00Z",
	    "position":{"new_path":"a.go","new_line":3,"head_sha":"d2"}}]},
	  {"id":"dh","notes":[{"id":52,"body":"a human","resolvable":true,"author":{"username":"eugene"},
	    "created_at":"2026-10-06T13:01:00Z","updated_at":"2026-10-06T13:01:00Z"}]}
	]
	[{"id":"db","notes":[{"id":53,"body":"leak","resolvable":true,"author":{"username":"claude"},
	    "created_at":"2026-10-06T13:02:00Z","updated_at":"2026-10-06T13:02:00Z",
	    "position":{"new_path":"b.go","new_line":9,"head_sha":"d2"}}]}]`)

	r, err := rv.Pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	if r.ID != "da" || r.SHA != "d2" || len(r.Host.Threads) != 2 || r.Handled {
		t.Errorf("picked %+v", r)
	}

	if !strings.Contains(r.Body, "### a.go:3\n\nnil map") || !strings.Contains(r.Body, "### b.go:9\n\nleak") ||
		strings.Contains(r.Body, "a human") {
		t.Errorf("body:\n%s", r.Body)
	}

	put := "glab api -X PUT projects/:fullpath/merge_requests/7/notes/51 -f body=nil map\n\n<!-- handled -->"
	f.Script(put, `{}`)

	if _, err := rv.Ack(t.Context(), r); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.Called(put) {
		t.Errorf("the marker must land on the first note alone: %v", f.Calls)
	}

	rv.Facts.Handled = facts.HandledThread
	for _, id := range []string{"da", "db"} {
		f.Script("glab api -X PUT projects/:fullpath/merge_requests/7/discussions/"+id+"?resolved=true", `{}`)
	}

	if what, err := rv.Ack(t.Context(), r); err != nil || what != "2 thread(s) resolved" {
		t.Errorf("thread ack = %q, %v", what, err)
	}
}

// A GitLab pipeline job named as the reviewer: its trace is the body once
// it finished.
func TestReviewGlabPipelineJob(t *testing.T) {
	f := testkit.NewFakes(t)

	rf := testkit.StickyFacts()
	rf.Reviewer, rf.ReviewerKind, rf.Arrives = "claude-review", facts.ReviewerCheck, facts.ArrivesCheck
	rf.Done, rf.Handled, rf.HandledText = facts.DoneConcluded, "", ""
	rv := glabReviewer(t, f, rf)

	f.Script("glab api --paginate projects/:fullpath/pipelines/77/jobs?per_page=100", `[
	  {"id":1,"name":"test","status":"success"},
	  {"id":2,"name":"claude-review","status":"success","web_url":"https://gitlab.com/g/p/-/jobs/2",
	   "created_at":"2026-10-06T13:00:00Z","finished_at":"2026-10-06T13:10:00Z"}
	]`)
	f.Script("glab api projects/:fullpath/jobs/2/trace", "a.go:3 nil map\n")

	r, err := rv.Pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	if r.ID != "2" || !r.Done || r.SHA != "d2" || r.Body != "a.go:3 nil map" {
		t.Errorf("picked %+v", r)
	}
}

// A comment's sha is the head when it was posted: one posted before the
// last commit is stale, one after it is not.
func TestReviewStaleAgainstTheHead(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	f.Script(testkit.CommentsPath, `[
	  {"id":100,"body":"at c2","html_url":"u1","created_at":"2026-10-06T13:00:00Z",
	   "updated_at":"2026-10-06T13:00:00Z","user":{"login":"claude[bot]"}},
	  {"id":200,"body":"at c3","html_url":"u2","created_at":"2026-10-06T15:00:00Z",
	   "updated_at":"2026-10-06T15:00:00Z","user":{"login":"claude[bot]"}}
	]`)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "100", 1), `[]`)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "200", 1), `[]`)

	found, err := rv.Candidates(t.Context())
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}

	stale := map[string]bool{}
	for _, r := range found {
		stale[r.SHA] = r.Stale
	}

	if !stale["c2"] || stale["c3"] {
		t.Errorf("stale by sha = %v, want c2 stale and c3 current", stale)
	}
}

// A sticky comment created before the last commit and rewritten after it
// reviewed the head: it is not stale.
func TestReviewStaleReadsAStickyCommentAtItsLastEdit(t *testing.T) {
	f := testkit.NewFakes(t)
	rv := testkit.GhReviewer(t, f, testkit.StickyFacts())

	f.Script(testkit.CommentsPath, `[{"id":100,"body":"rewritten","html_url":"u","created_at":"2026-10-06T11:00:00Z",
	  "updated_at":"2026-10-06T15:00:00Z","user":{"login":"claude[bot]"}}]`)
	f.Script("gh api user", testkit.ViewerJSON)
	f.Script(strings.Replace(testkit.ReactionsPath, "%s", "100", 1), `[]`)

	found, err := rv.Candidates(t.Context())
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}

	if len(found) != 1 || found[0].Stale || found[0].SHA != "c1" {
		t.Errorf("found = %+v, want one review at c1 that is not stale", found)
	}

	if found[0].Triggerable {
		t.Error("an automatic-only trigger reads as triggerable")
	}
}
