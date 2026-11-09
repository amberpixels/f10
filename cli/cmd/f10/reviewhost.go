package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/forge"
)

// The host's side of `f10 review`: the reviews a declared reviewer left on a
// branch's request, in whichever of the three places the project said they
// arrive.

// A remoteReview is one review normalized across the three arrival kinds and
// both CLIs; the exported fields are the --json output, the rest is for ack.
type remoteReview struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	SHA       string    `json:"sha,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Done      bool      `json:"done"`
	Handled   bool      `json:"handled"`
	Settled   bool      `json:"settled"`
	Stale     bool      `json:"stale"` // commits landed on the request after the sha it reviewed

	// Triggerable says `f10 review trigger` can perform the declared trigger:
	// the step offers a new review only when it is true.
	Triggerable bool   `json:"triggerable"`
	Revision    string `json:"revision"`

	// stored is the host's text on the object ack edits: Body folds an inline
	// review's comments in, and writing that back would copy them into the summary
	stored string

	threads    []string // inline review: the threads a `thread resolved` ack resolves
	threadsErr error    // inline review: why threads could not be read, reported by the ack
	commentID  string   // inline review: the comment a reaction or marker lands on
	pending    bool     // inline review: not yet submitted
	concluded  bool     // check run: finished
}

// A reviewPull is the request the reviews sit on, with the commits a
// comment's sha is derived from (an issue comment stores none).
type reviewPull struct {
	Number   string
	HeadSHA  string
	URL      string
	Commits  []pullCommit
	Pipeline *headPipeline // glab only; nil until a pipeline has run
}

// headPipeline is the merge request's latest pipeline, as GitLab records it
// on the request itself: the pipelines listing has no sort order to ask for.
type headPipeline struct {
	ID  int64  `json:"id"`
	SHA string `json:"sha"`
}

type pullCommit struct {
	SHA string
	At  time.Time
}

// shaBefore is the head a reviewer saw when it posted at t; the head sha when
// no commit precedes it.
func (p reviewPull) shaBefore(t time.Time) string {
	sha := p.HeadSHA

	for _, c := range p.Commits {
		if !c.At.After(t) {
			sha = c.SHA
		}
	}

	return sha
}

// errNoReview is pick finding nothing to return: no review by the reviewer,
// or none left unhandled. wait keeps polling on it and nothing else.
var errNoReview = errors.New("no review")

type reviewer struct {
	h     host
	facts facts.Review
	pull  reviewPull

	reactions map[string]map[string]bool // per candidates call: review id to the viewer's fresh reactions

	viewer string // the login gh or glab is signed in as
	owner  string // gh GraphQL needs owner and name spelled out
	repo   string
}

// newReviewer reads the request number names, or the current branch's.
//
//nolint:lostfield // h is kept whole on the reviewer
func newReviewer(ctx context.Context, h host, f facts.Review, number string) (*reviewer, error) {
	rv := &reviewer{h: h, facts: f}

	if err := rv.loadPull(ctx, number); err != nil {
		return nil, err
	}

	return rv, nil
}

// isReviewer drops a declared `[bot]` on GitLab, which names a bot by username
// alone with no suffix anyone could forge; on GitHub SameLogin's rule stands.
func (rv *reviewer) isReviewer(login string) bool {
	declared := rv.facts.Reviewer
	if rv.h.GLab() {
		declared = strings.TrimSuffix(declared, "[bot]")
	}

	return forge.SameLogin(declared, login)
}

// candidates lists every review, newest update first. A settled one counts as
// handled: the settled marker is an edit, which would otherwise make the
// earlier handled reaction read as stale.
func (rv *reviewer) candidates(ctx context.Context) ([]remoteReview, error) {
	var (
		found []remoteReview
		err   error
	)

	switch rv.facts.Arrives {
	case facts.ArrivesComment:
		found, err = rv.comments(ctx)
	case facts.ArrivesInline:
		found, err = rv.inlineReviews(ctx)
	case facts.ArrivesCheck:
		found, err = rv.checkRuns(ctx)
	default:
		return nil, fmt.Errorf("the Review section declares no known arrival kind: %q", rv.facts.Arrives)
	}

	if err != nil {
		return nil, err
	}

	rv.reactions = map[string]map[string]bool{}

	for i := range found {
		found[i].Done = rv.isDone(found[i])

		if found[i].Handled, err = rv.isHandled(ctx, found[i]); err != nil {
			return nil, err
		}

		if found[i].Settled, err = rv.isSettled(ctx, found[i]); err != nil {
			return nil, err
		}

		found[i].Handled = found[i].Handled || found[i].Settled
		found[i].Stale = rv.stale(found[i])
		_, _, found[i].Triggerable = rv.facts.ManualAction()
		found[i].Revision = rv.revision(found[i])
	}

	sort.SliceStable(found, func(i, j int) bool { return found[i].UpdatedAt.After(found[j].UpdatedAt) })

	return found, nil
}

// stale judges an issue comment at its last edit: a sticky comment rewritten
// by a re-review keeps its first post's sha but reviewed the newer head.
func (rv *reviewer) stale(r remoteReview) bool {
	sha := r.SHA
	if r.Kind == facts.ArrivesComment {
		sha = rv.pull.shaBefore(r.UpdatedAt)
	}

	return sha != "" && rv.pull.HeadSHA != "" && sha != rv.pull.HeadSHA
}

// pick is the newest review the reviewer left that nobody handled yet.
func (rv *reviewer) pick(ctx context.Context) (remoteReview, error) {
	found, err := rv.candidates(ctx)
	if err != nil {
		return remoteReview{}, err
	}

	if len(found) == 0 {
		return remoteReview{}, fmt.Errorf("%w from %s on %s%s",
			errNoReview, rv.facts.Reviewer, rv.pull.URL, rv.manualHint())
	}

	if r, ok := firstUnhandled(found); ok {
		return r, nil
	}

	return remoteReview{}, fmt.Errorf("%w left unhandled from %s: the latest (%s) is handled%s",
		errNoReview, rv.facts.Reviewer, found[0].URL, rv.manualHint())
}

// latest is the newest review whatever its state: the one resolve reads.
func (rv *reviewer) latest(ctx context.Context) (remoteReview, error) {
	found, err := rv.candidates(ctx)
	if err != nil {
		return remoteReview{}, err
	}

	if len(found) == 0 {
		return remoteReview{}, fmt.Errorf("%w from %s on %s%s",
			errNoReview, rv.facts.Reviewer, rv.pull.URL, rv.manualHint())
	}

	return found[0], nil
}

// ackTarget is the review id names, else pick's, else with every review
// handled the newest, which ack confirms rather than marks twice.
func (rv *reviewer) ackTarget(ctx context.Context, id string) (remoteReview, error) {
	found, err := rv.candidates(ctx)
	if err != nil {
		return remoteReview{}, err
	}

	if len(found) == 0 {
		return remoteReview{}, fmt.Errorf("nothing to acknowledge: %w from %s on %s",
			errNoReview, rv.facts.Reviewer, rv.pull.URL)
	}

	if id != "" {
		for _, r := range found {
			if r.ID == id {
				return r, nil
			}
		}

		return remoteReview{}, fmt.Errorf("nothing to acknowledge: %w %s from %s on %s",
			errNoReview, id, rv.facts.Reviewer, rv.pull.URL)
	}

	if r, ok := firstUnhandled(found); ok {
		return r, nil
	}

	return found[0], nil
}

// revision fingerprints the text without f10's marks: a sticky comment
// rewritten by a re-review keeps its id and first sha, so only its text tells
// the new review apart.
func (rv *reviewer) revision(r remoteReview) string {
	body := checkboxRE.ReplaceAllStringFunc(r.Body, func(box string) string {
		return strings.NewReplacer("[x]", "[ ]", "[X]", "[ ]").Replace(box)
	})

	for _, mark := range []facts.Mark{rv.facts.HandledMark(), rv.facts.SettledMark()} {
		if mark.Kind == facts.HandledMarker && mark.Text != "" {
			body = strings.ReplaceAll(body, mark.Text, "")
		}
	}

	// ack's marker arrives after a blank line, mid-text on an inline review
	// whose comments are folded in after it: whitespace is not the review
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(body), " ")))

	return hex.EncodeToString(sum[:6])
}

// settleTarget refuses a review now at another revision: a re-review rewrote
// it, and marking it would settle findings nobody read.
func (rv *reviewer) settleTarget(ctx context.Context, id, revision string) (remoteReview, error) {
	found, err := rv.candidates(ctx)
	if err != nil {
		return remoteReview{}, err
	}

	for _, r := range found {
		if r.ID != id {
			continue
		}

		if r.Revision != revision {
			return remoteReview{}, fmt.Errorf(
				"%w: %s is at revision %s, not %s; nothing marked, the new review is unread",
				errReviewChanged,
				r.URL,
				r.Revision,
				revision,
			)
		}

		return r, nil
	}

	return remoteReview{}, fmt.Errorf("nothing to settle: %w %s from %s on %s",
		errNoReview, id, rv.facts.Reviewer, rv.pull.URL)
}

// firstUnhandled serves pick and ack, so the two verbs always name the same
// review.
func firstUnhandled(found []remoteReview) (remoteReview, bool) {
	for _, r := range found {
		if !r.Handled {
			return r, true
		}
	}

	return remoteReview{}, false
}

func (rv *reviewer) manualHint() string {
	if !strings.Contains(rv.facts.Trigger, facts.TriggerManual) {
		return ""
	}

	how := rv.facts.Manual()
	if how == "" {
		how = "see project.md"
	}

	who := "f10 never performs it"
	if _, _, ok := rv.facts.ManualAction(); ok {
		who = "`f10 review trigger` performs it"
	}

	if strings.Contains(rv.facts.Trigger, facts.TriggerAutomatic) {
		return "; a re-review is manual (" + how + ") and " + who
	}

	return "; the trigger is manual (" + how + ") and " + who
}

// trigger returns what it did, where, and the host's time for it, which a new
// review must postdate.
func (rv *reviewer) trigger(ctx context.Context) (string, string, time.Time, error) {
	if !strings.Contains(rv.facts.Trigger, facts.TriggerManual) {
		return "", "", time.Time{}, errors.New("the Review section's Trigger is automatic only: " +
			"a push fires the review, and there is no manual trigger to perform")
	}

	kind, text, ok := rv.facts.ManualAction()
	if !ok {
		return "", "", time.Time{}, fmt.Errorf("the Review section's Trigger how (%q) reads as neither "+
			"a comment (comment `<text>`) nor a review request (request a review from <login>): "+
			"perform it by hand", rv.facts.Manual())
	}

	if kind == facts.ManualRequest {
		return rv.requestReview(ctx, text)
	}

	path := rv.issuePath("issues/" + rv.pull.Number + "/comments")
	if rv.h.GLab() {
		path = rv.mrPath("notes")
	}

	var posted struct {
		ID        int64     `json:"id"`
		HTMLURL   string    `json:"html_url"`
		CreatedAt time.Time `json:"created_at"`
	}

	if err := rv.h.Decode(ctx, &posted, "api", "-X", "POST", path, "-f", "body="+text); err != nil {
		return "", "", time.Time{}, err
	}

	url := cmp.Or(posted.HTMLURL, rv.pull.URL+"#note_"+strconv.FormatInt(posted.ID, 10))

	return "commented " + text, url, cmp.Or(posted.CreatedAt, time.Now().UTC()), nil
}

func (rv *reviewer) isDone(r remoteReview) bool {
	switch rv.facts.Done {
	case facts.DoneMarker:
		return rv.facts.DoneText != "" && strings.Contains(r.Body, rv.facts.DoneText)
	case facts.DoneSubmitted:
		return !r.pending
	case facts.DoneConcluded:
		return r.concluded
	default: // posted: it exists
		return r.Kind != facts.ArrivesCheck || r.concluded
	}
}

var checkboxRE = regexp.MustCompile(`(?m)^\s*[-*]\s+\[([ xX])\]`)

// isHandled counts a reaction only when newer than the review's last edit: a
// sticky comment keeps its id across re-reviews, so round one's reaction must
// not handle round two.
func (rv *reviewer) isHandled(ctx context.Context, r remoteReview) (bool, error) {
	switch rv.facts.Handled {
	case facts.HandledReaction, facts.HandledMarker:
		return rv.hasMark(ctx, r, rv.facts.HandledMark())
	case facts.HandledCheckbox:
		m := checkboxRE.FindStringSubmatch(r.stored)

		return len(m) > 1 && m[1] != " ", nil
	case facts.HandledThread:
		return rv.threadsResolved(ctx, r)
	default:
		return false, nil
	}
}

// isSettled: none declared, or a check run, is never settled on the host.
func (rv *reviewer) isSettled(ctx context.Context, r remoteReview) (bool, error) {
	if r.Kind == facts.ArrivesCheck {
		return false, nil
	}

	return rv.hasMark(ctx, r, rv.facts.SettledMark())
}

// hasMark reads a reaction or a marker, the two kinds handled and settled
// share; any other kind reads as absent.
func (rv *reviewer) hasMark(ctx context.Context, r remoteReview, m facts.Mark) (bool, error) {
	switch m.Kind {
	case facts.HandledReaction:
		return rv.hasFreshReaction(ctx, r, m.Text)
	case facts.HandledMarker:
		return m.Text != "" && strings.Contains(r.stored, m.Text), nil
	default:
		return false, nil
	}
}

// leaveMark posts a reaction or appends a marker; ok is false for any other
// kind, which the caller handles or refuses.
func (rv *reviewer) leaveMark(ctx context.Context, r remoteReview, m facts.Mark) (string, bool, error) {
	switch m.Kind {
	case facts.HandledReaction:
		msg, err := rv.react(ctx, r, m.Text)

		return msg, true, err
	case facts.HandledMarker:
		msg, err := rv.patchBody(ctx, r, strings.TrimRight(r.stored, "\n")+"\n\n"+m.Text, "marker appended")

		return msg, true, err
	default:
		return "", false, nil
	}
}

func (rv *reviewer) hasFreshReaction(ctx context.Context, r remoteReview, emoji string) (bool, error) {
	if r.Kind == facts.ArrivesCheck {
		return false, nil
	}

	if fresh, ok := rv.reactions[r.ID]; ok {
		return fresh[emoji], nil
	}

	fresh, err := rv.freshReactions(ctx, r)
	if err != nil {
		return false, err
	}

	if rv.reactions != nil {
		rv.reactions[r.ID] = fresh
	}

	return fresh[emoji], nil
}

// ack confirms an already handled review rather than posting twice.
func (rv *reviewer) ack(ctx context.Context, r remoteReview) (string, error) {
	if r.Handled {
		return "already handled", nil
	}

	if r.Kind == facts.ArrivesCheck {
		return "a check run carries no handled marker: a new push starts a new run", nil
	}

	if msg, ok, err := rv.leaveMark(ctx, r, rv.facts.HandledMark()); ok {
		return msg, err
	}

	switch rv.facts.Handled {
	case facts.HandledCheckbox:
		// the first box only: isHandled reads the first, and the rest may be
		// the reviewer's own list, which ticking would read as all fixed
		m := checkboxRE.FindStringSubmatchIndex(r.stored)
		if m == nil {
			return "", errors.New("the review carries no checkbox to tick")
		}

		body := r.stored[:m[2]] + "x" + r.stored[m[3]:]

		return rv.patchBody(ctx, r, body, "checkbox ticked")
	case facts.HandledThread:
		return rv.resolveThreads(ctx, r)
	default:
		return "", fmt.Errorf("the Review section declares no known handled marker: %q", rv.facts.Handled)
	}
}

// settle leaves the declared settled mark: resolve's word that every finding
// has a verdict.
func (rv *reviewer) settle(ctx context.Context, r remoteReview) (string, error) {
	if r.Settled {
		return "already settled", nil
	}

	if r.Kind == facts.ArrivesCheck {
		return "a check run carries no settled marker: the findings file is the record", nil
	}

	if msg, ok, err := rv.leaveMark(ctx, r, rv.facts.SettledMark()); ok {
		return msg, err
	}

	return "", errors.New("project.md declares no review settled mark: declare `settled` " +
		"(reaction `<emoji>` or marker \"<text>\") under review")
}
