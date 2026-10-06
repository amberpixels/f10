package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// The review noun is exercised against a scripted gh or glab: what each
// verb asks the host and what it concludes, with the facts declared the way
// f10 init writes them.

const (
	reviewPR = "gh pr view --json number,url,headRefOid,commits"
	prJSON   = `{"number":5,"url":"https://github.com/o/r/pull/5","headRefOid":"c3",` +
		`"commits":[{"oid":"c1","committedDate":"2026-10-06T10:00:00Z"},` +
		`{"oid":"c2","committedDate":"2026-10-06T12:00:00Z"},{"oid":"c3","committedDate":"2026-10-06T14:00:00Z"}]}`

	commentsPath  = "gh api repos/{owner}/{repo}/issues/5/comments?per_page=100"
	reactionsPath = "gh api repos/{owner}/{repo}/issues/comments/%s/reactions?per_page=100"
	viewerJSON    = `{"login":"eugene"}`
)

func stickyFacts() facts.Review {
	return facts.Review{
		Reviewer: "claude[bot]", ReviewerKind: facts.ReviewerBot, Arrives: facts.ArrivesComment,
		Trigger: facts.TriggerAutomatic, TriggerHow: "on open and on push",
		Done: facts.DonePosted, Handled: facts.HandledReaction, HandledText: "eyes",
	}
}

func ghReviewer(t *testing.T, f *fakes, rf facts.Review) *reviewer {
	t.Helper()

	f.script(reviewPR, prJSON)

	rv, err := newReviewer(t.Context(), host{name: "gh", dir: "."}, rf, "")
	if err != nil {
		t.Fatalf("newReviewer: %v", err)
	}

	return rv
}

// Two reviews by the bot: an older one the viewer reacted to after its last
// edit (handled), and a sticky one re-edited after the reaction (unhandled
// again, the way a re-review must read). Pick returns the sticky one with
// the sha the PR had when it was posted.
func TestReviewPickSkipsHandledAndSeesThroughStaleReactions(t *testing.T) {
	f := newFakes(t)
	rv := ghReviewer(t, f, stickyFacts())

	f.script(commentsPath, `[
	  {"id":100,"body":"round one","html_url":"https://github.com/o/r/pull/5#issuecomment-100",
	   "created_at":"2026-10-06T11:00:00Z","updated_at":"2026-10-06T11:00:00Z","user":{"login":"claude[bot]"}},
	  {"id":200,"body":"round two","html_url":"https://github.com/o/r/pull/5#issuecomment-200",
	   "created_at":"2026-10-06T13:00:00Z","updated_at":"2026-10-06T15:00:00Z","user":{"login":"claude[bot]"}},
	  {"id":300,"body":"a human","html_url":"x","created_at":"2026-10-06T16:00:00Z","updated_at":"2026-10-06T16:00:00Z",
	   "user":{"login":"eugene"}}
	]`)
	f.script("gh api user", viewerJSON)
	f.script(strings.Replace(reactionsPath, "%s", "100", 1),
		`[{"content":"eyes","created_at":"2026-10-06T11:30:00Z","user":{"login":"eugene"}}]`)
	f.script(strings.Replace(reactionsPath, "%s", "200", 1),
		`[{"content":"eyes","created_at":"2026-10-06T14:30:00Z","user":{"login":"eugene"}},
		  {"content":"eyes","created_at":"2026-10-06T15:30:00Z","user":{"login":"someone-else"}}]`)

	r, err := rv.pick(t.Context())
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

	doc := reviewDoc(r)
	for _, want := range []string{"# review by claude[bot] · issue comment", "https://github.com/o/r/pull/5#issuecomment-200", "c2", "round two"} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc missing %q:\n%s", want, doc)
		}
	}
}

func TestReviewPickNamesTheManualTriggerWhenNothingIsUnhandled(t *testing.T) {
	f := newFakes(t)

	rf := stickyFacts()
	rf.Trigger, rf.TriggerHow = facts.TriggerManual, "comment `@claude` on the PR"
	rv := ghReviewer(t, f, rf)

	f.script(commentsPath, `[{"id":100,"body":"done","html_url":"u","created_at":"2026-10-06T11:00:00Z",
	  "updated_at":"2026-10-06T11:00:00Z","user":{"login":"claude[bot]"}}]`)
	f.script("gh api user", viewerJSON)
	f.script(strings.Replace(reactionsPath, "%s", "100", 1),
		`[{"content":"eyes","created_at":"2026-10-06T11:30:00Z","user":{"login":"eugene"}}]`)

	_, err := rv.pick(t.Context())
	if !errors.Is(err, errNoReview) {
		t.Fatalf("err = %v, want errNoReview", err)
	}

	for _, want := range []string{"the trigger is manual", "@claude", "never performs it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	// nothing posted at all is the other refusal, with the same hint
	f.script(commentsPath, `[]`)

	_, err = rv.pick(t.Context())
	if !errors.Is(err, errNoReview) || !strings.Contains(err.Error(), "the trigger is manual") {
		t.Errorf("err = %v", err)
	}
}

// The second acceptance criterion: a review that never completes returns
// within the budget and says so.
func TestAwaitReviewGivesUpWithinBudget(t *testing.T) {
	f := newFakes(t)
	rv := ghReviewer(t, f, stickyFacts())
	f.script(commentsPath, `[]`)

	start := time.Now()

	_, err := awaitReview(t.Context(), rv, 40*time.Millisecond, 5*time.Millisecond)
	if err == nil {
		t.Fatal("wait returned a review from an empty thread")
	}

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("wait took %s on a 40ms budget", elapsed)
	}

	for _, want := range []string{"no completed review from claude[bot] within 40ms", "no review from claude[bot]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	// several polls happened, not one
	polls := 0

	for _, c := range f.calls {
		if c == commentsPath {
			polls++
		}
	}

	if polls < 2 {
		t.Errorf("comments were read %d time(s), want repeated polling", polls)
	}
}

func TestAwaitReviewReturnsOnceDone(t *testing.T) {
	f := newFakes(t)

	rf := stickyFacts()
	rf.Done, rf.DoneText = facts.DoneMarker, "Review complete"
	rv := ghReviewer(t, f, rf)

	running := `[{"id":100,"body":"Reviewing...","html_url":"u","created_at":"2026-10-06T11:00:00Z",
	  "updated_at":"2026-10-06T11:00:00Z","user":{"login":"claude[bot]"}}]`
	finished := strings.Replace(running, "Reviewing...", "Findings... Review complete", 1)

	f.script(commentsPath, running, running, finished)
	f.script("gh api user", viewerJSON)
	f.script(strings.Replace(reactionsPath, "%s", "100", 1), `[]`)

	r, err := awaitReview(t.Context(), rv, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	if !r.Done || !strings.Contains(r.Body, "Review complete") {
		t.Errorf("wait returned %+v before the marker", r)
	}
}

func TestReviewAckReactsOnceAndSkipsCheckRuns(t *testing.T) {
	f := newFakes(t)
	rv := ghReviewer(t, f, stickyFacts())

	unhandled := remoteReview{ID: "200", Kind: facts.ArrivesComment, URL: "u"}
	post := "gh api -X POST repos/{owner}/{repo}/issues/comments/200/reactions -f content=eyes"
	f.script(post, `{"id":1}`)

	what, err := rv.ack(t.Context(), unhandled)
	if err != nil || what != "reaction eyes" {
		t.Errorf("ack = %q, %v", what, err)
	}

	if !f.called(post) {
		t.Errorf("no reaction posted: %v", f.calls)
	}

	before := len(f.calls)

	if what, err := rv.ack(
		t.Context(),
		remoteReview{ID: "200", Handled: true},
	); err != nil ||
		what != "already handled" {
		t.Errorf("ack on handled = %q, %v", what, err)
	}

	what, err = rv.ack(t.Context(), remoteReview{ID: "9", Kind: facts.ArrivesCheck})
	if err != nil || !strings.Contains(what, "no handled marker") {
		t.Errorf("ack on a check run = %q, %v", what, err)
	}

	if len(f.calls) != before {
		t.Errorf("a confirmed or check-run ack reached the host: %v", f.calls[before:])
	}
}

func TestReviewAckTicksTheCheckboxAndAppendsTheMarker(t *testing.T) {
	f := newFakes(t)

	rf := stickyFacts()
	rf.Handled, rf.HandledText = facts.HandledCheckbox, ""
	rv := ghReviewer(t, f, rf)

	patch := "gh api -X PATCH repos/{owner}/{repo}/issues/comments/7 -f body=findings\n\n- [x] handled"
	f.script(patch, `{}`)

	if _, err := rv.ack(
		t.Context(),
		remoteReview{ID: "7", Kind: facts.ArrivesComment, Body: "findings\n\n- [ ] handled"},
	); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.called(patch) {
		t.Errorf("checkbox not ticked: %v", f.calls)
	}

	if _, err := rv.ack(t.Context(), remoteReview{ID: "7", Kind: facts.ArrivesComment, Body: "no box"}); err == nil {
		t.Error("ack ticked a checkbox that is not there")
	}

	rv.facts.Handled, rv.facts.HandledText = facts.HandledMarker, "<!-- handled -->"
	marker := "gh api -X PATCH repos/{owner}/{repo}/issues/comments/7 -f body=findings\n\n<!-- handled -->"
	f.script(marker, `{}`)

	if _, err := rv.ack(
		t.Context(),
		remoteReview{ID: "7", Kind: facts.ArrivesComment, Body: "findings\n"},
	); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.called(marker) {
		t.Errorf("marker not appended: %v", f.calls)
	}
}

// The same noun over glab: merge request, notes, award emoji.
func TestReviewGlabSpelling(t *testing.T) {
	f := newFakes(t)

	f.script("glab mr view -F json", `{"iid":7,"web_url":"https://gitlab.com/g/p/-/merge_requests/7","sha":"d2"}`)
	f.script("glab api projects/:fullpath/merge_requests/7/commits?per_page=100",
		`[{"id":"d1","committed_date":"2026-10-06T10:00:00Z"},{"id":"d2","committed_date":"2026-10-06T12:00:00Z"}]`)

	rv, err := newReviewer(t.Context(), host{name: "glab", dir: "."}, stickyFacts(), "")
	if err != nil {
		t.Fatalf("newReviewer: %v", err)
	}

	f.script("glab api projects/:fullpath/merge_requests/7/notes?per_page=100", `[
	  {"id":40,"body":"merged the branch","system":true,"author":{"username":"claude"},
	   "created_at":"2026-10-06T11:00:00Z","updated_at":"2026-10-06T11:00:00Z"},
	  {"id":41,"body":"findings","system":false,"author":{"username":"claude"},
	   "created_at":"2026-10-06T11:00:00Z","updated_at":"2026-10-06T11:00:00Z"}
	]`)
	f.script("glab api user", `{"username":"eugene"}`)
	f.script("glab api projects/:fullpath/merge_requests/7/notes/41/award_emoji?per_page=100", `[]`)

	r, err := rv.pick(t.Context())
	if err != nil {
		t.Fatalf("pick: %v", err)
	}

	if r.ID != "41" || r.URL != "https://gitlab.com/g/p/-/merge_requests/7#note_41" || r.SHA != "d1" {
		t.Errorf("picked %+v", r)
	}

	award := "glab api -X POST projects/:fullpath/merge_requests/7/notes/41/award_emoji -f name=eyes"
	f.script(award, `{}`)

	if _, err := rv.ack(t.Context(), r); err != nil {
		t.Fatalf("ack: %v", err)
	}

	if !f.called(award) {
		t.Errorf("no award emoji posted: %v", f.calls)
	}
}

// The refusals, before any host call: no remote reviewer declared, a fact
// outside the vocabulary, a marker that cannot apply where the review lands.
func TestReviewRefusesOnTheDeclaration(t *testing.T) {
	isolateEnv(t)

	prev := shell.Capture
	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		t.Errorf("host CLI reached before the refusal: %s %s", name, strings.Join(args, " "))

		return shell.Result{}, nil
	}
	t.Cleanup(func() { shell.Capture = prev })

	cases := []struct{ name, review, want string }{
		{name: "no Review section", review: "", want: "declares no remote review"},
		{
			name:   "local-only section",
			review: "## Review\n\n- **Never flag** - vendored code\n",
			want:   "declares no remote review",
		},
		{
			name: "a fact outside the vocabulary",
			review: "## Review\n\n- **Reviewer** - bot `claude[bot]`\n- **Arrives as** - by pigeon\n" +
				"- **Trigger** - automatic\n- **Done, handled** - posted, reaction `eyes`\n",
			want: "Arrives as line(s) could not be read",
		},
		{
			name: "threads on a comment",
			review: "## Review\n\n- **Reviewer** - bot `claude[bot]`\n- **Arrives as** - issue comment\n" +
				"- **Trigger** - automatic\n- **Done, handled** - posted, thread resolved\n",
			want: "does not arrive inline",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := mkRepo(t)
			dir := filepath.Join(repo, ".f10", "instructions")

			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}

			md := "# demo\n\n## Tracker\n\nGitHub Issues. Task ids `GH-###`.\n\n" + c.review
			if err := os.WriteFile(filepath.Join(dir, "project.md"), []byte(md), 0o600); err != nil {
				t.Fatal(err)
			}

			app := newApp()
			app.Writer = &bytes.Buffer{}

			err := app.Run(t.Context(), []string{"f10", "-C", repo, "review", "pick"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}
