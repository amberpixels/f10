package hostops_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/reviews"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The same noun over glab: merge request, notes, award emoji.
func TestReviewGlabSpelling(t *testing.T) {
	f := testkit.NewFakes(t)

	f.Script("glab mr view -F json", `{"iid":7,"web_url":"https://gitlab.com/g/p/-/merge_requests/7","sha":"d2"}`)
	f.Script("glab api --paginate projects/:fullpath/merge_requests/7/commits?per_page=100",
		`[{"id":"d1","committed_date":"2026-10-06T10:00:00Z"},{"id":"d2","committed_date":"2026-10-06T12:00:00Z"}]`)

	rv, err := reviews.NewReviewer(t.Context(), hostops.NewHost("glab", "."), testkit.StickyFacts(), "")
	if err != nil {
		t.Fatalf("newReviewer: %v", err)
	}

	f.Script("glab api --paginate projects/:fullpath/merge_requests/7/notes?per_page=100", `[
	  {"id":40,"body":"merged the branch","system":true,"author":{"username":"claude"},
	   "created_at":"2026-10-06T11:00:00Z","updated_at":"2026-10-06T11:00:00Z"},
	  {"id":41,"body":"findings","system":false,"author":{"username":"claude"},
	   "created_at":"2026-10-06T11:00:00Z","updated_at":"2026-10-06T11:00:00Z"}
	]`)
	f.Script("glab api user", `{"username":"eugene"}`)
	f.Script("glab api projects/:fullpath/merge_requests/7/notes/41/award_emoji?per_page=100", `[]`)

	r, err := rv.Pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	if r.ID != "41" || r.URL != "https://gitlab.com/g/p/-/merge_requests/7#note_41" || r.SHA != "d1" {
		t.Errorf("picked %+v", r)
	}

	award := "glab api -X POST projects/:fullpath/merge_requests/7/notes/41/award_emoji -f name=eyes"
	f.Script(award, `{}`)

	if _, err := rv.Ack(t.Context(), r); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.Called(award) {
		t.Errorf("no award emoji posted: %v", f.Calls)
	}
}

func TestReviewTriggerGlabSpelling(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("glab mr view -F json", `{"iid":7,"web_url":"https://gitlab.com/o/r/-/merge_requests/7","sha":"c3"}`)
	f.Script("glab api --paginate projects/:fullpath/merge_requests/7/commits?per_page=100", `[]`)

	rf := testkit.StickyFacts()
	rf.Trigger, rf.TriggerHow = facts.TriggerManual, "comment `@claude` on the MR"

	rv, err := reviews.NewReviewer(t.Context(), hostops.NewHost("glab", "."), rf, "")
	if err != nil {
		t.Fatalf("newReviewer: %v", err)
	}

	f.Script("glab api -X POST projects/:fullpath/merge_requests/7/notes -f body=@claude",
		`{"id":41,"created_at":"2026-10-06T16:00:00Z"}`)

	_, url, _, err := rv.Trigger(t.Context())
	if err != nil || url != "https://gitlab.com/o/r/-/merge_requests/7#note_41" {
		t.Errorf("trigger url = %q, %v", url, err)
	}

	rv.Facts.TriggerHow = "request a review from `octocat`"
	f.Script("glab mr update 7 --reviewer +octocat", "")

	if _, _, _, err = rv.Trigger(t.Context()); err != nil {
		t.Errorf("request trigger: %v", err)
	}
}
