package testkit

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/reviews"
)

// The review noun against a scripted gh or glab, with the facts declared the
// way f10 init writes them.

const (
	reviewPR = "gh pr view --json number,url,headRefOid,commits"
	prJSON   = `{"number":5,"url":"https://github.com/o/r/pull/5","headRefOid":"c3",` +
		`"commits":[{"oid":"c1","committedDate":"2026-10-06T10:00:00Z"},` +
		`{"oid":"c2","committedDate":"2026-10-06T12:00:00Z"},{"oid":"c3","committedDate":"2026-10-06T14:00:00Z"}]}`

	CommentsPath  = "gh api --paginate repos/{owner}/{repo}/issues/5/comments?per_page=100"
	ReactionsPath = "gh api repos/{owner}/{repo}/issues/comments/%s/reactions?per_page=100"
	ViewerJSON    = `{"login":"eugene"}`
)

func StickyFacts() facts.Review {
	return facts.Review{
		Reviewer: "claude[bot]", ReviewerKind: facts.ReviewerBot, Arrives: facts.ArrivesComment,
		Trigger: facts.TriggerAutomatic, TriggerHow: "on open and on push",
		Done: facts.DonePosted, Handled: facts.HandledReaction, HandledText: "eyes",
	}
}

// GhReviewer is a gh reviewer over f, with the review the facts declare.
//
//nolint:lostfield // builds a reviewer from the facts, not a conversion
func GhReviewer(t *testing.T, f *Fakes, rf facts.Review) *reviews.Reviewer {
	t.Helper()

	f.Script(reviewPR, prJSON)

	rv, err := reviews.NewReviewer(t.Context(), hostops.NewHost("gh", "."), rf, "")
	if err != nil {
		t.Fatalf("newReviewer: %v", err)
	}

	return rv
}

func SettledFacts() facts.Review {
	rf := StickyFacts()
	rf.Settled, rf.SettledText = facts.HandledReaction, "rocket"

	return rf
}
