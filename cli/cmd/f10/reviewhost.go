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

// errNoReview is pick finding nothing to return: no review by the reviewer,
// or none left unhandled. wait keeps polling on it and nothing else.
var errNoReview = errors.New("no review")

type reviewer struct {
	h     host
	facts facts.Review
	pull  *forge.ReviewPR

	reactions map[string]map[string]bool // per candidates call: review id to the viewer's fresh reactions
}

// newReviewer reads the request number names, or the current branch's.
//
//nolint:lostfield // h is kept whole on the reviewer
func newReviewer(ctx context.Context, h host, f facts.Review, number string) (*reviewer, error) {
	pull, err := h.OpenReviews(ctx, number)
	if err != nil {
		return nil, err
	}

	return &reviewer{h: h, facts: f, pull: pull}, nil
}

func (rv *reviewer) isReviewer(login string) bool { return rv.h.IsLogin(rv.facts.Reviewer, login) }

// candidates lists every review, newest update first. A settled one counts as
// handled: the settled marker is an edit, which would otherwise make the
// earlier handled reaction read as stale.
func (rv *reviewer) candidates(ctx context.Context) ([]remoteReview, error) {
	var (
		found []remoteReview
		err   error
	)

	var notes []forge.Note

	switch rv.facts.Arrives {
	case facts.ArrivesComment:
		notes, err = rv.pull.Comments(ctx, rv.isReviewer)
	case facts.ArrivesInline:
		notes, err = rv.pull.InlineReviews(ctx, rv.isReviewer)
	case facts.ArrivesCheck:
		notes, err = rv.pull.Checks(ctx, rv.facts.Reviewer)
	default:
		return nil, fmt.Errorf("the Review section declares no known arrival kind: %q", rv.facts.Arrives)
	}

	if err != nil {
		return nil, err
	}

	for _, n := range notes {
		found = append(found, fromNote(rv.facts, n))
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
		sha = rv.pull.ShaBefore(r.UpdatedAt)
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
		// neither host dates the request, so the local clock stands in; a
		// human's review comes long after
		at := time.Now().UTC().Truncate(time.Second)
		if err := rv.pull.RequestReview(ctx, text); err != nil {
			return "", "", time.Time{}, err
		}

		return "requested a review from " + text, rv.pull.URL, at, nil
	}

	url, at, err := rv.pull.Comment(ctx, text)
	if err != nil {
		return "", "", time.Time{}, err
	}

	return "commented " + text, url, cmp.Or(at, time.Now().UTC()), nil
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
		return rv.pull.ThreadsResolved(ctx, r.note())
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

	fresh, err := rv.pull.FreshReactions(ctx, r.note())
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

// fromNote is a host note as the review the declared arrival kind makes of
// it; GitLab discussions name no author, so the reviewer stands in.
//
//nolint:lostfield // candidates fills the judged fields: Done, Handled, Settled, Stale...
func fromNote(f facts.Review, n forge.Note) remoteReview {
	kind := f.Arrives
	if kind == facts.ArrivesInline && n.Author == "" {
		n.Author = f.Reviewer
	}

	return remoteReview{
		ID:         n.ID,
		Kind:       kind,
		URL:        n.URL,
		Author:     n.Author,
		Body:       n.Body,
		SHA:        n.SHA,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
		stored:     n.Stored,
		threads:    n.Threads,
		threadsErr: n.ThreadsErr,
		commentID:  n.CommentID,
		pending:    n.Pending,
		concluded:  n.Concluded,
	}
}

// note is the review as the host keeps it, for a write back to it.
func (r remoteReview) note() forge.Note {
	return forge.Note{
		ID:         r.ID,
		URL:        r.URL,
		Author:     r.Author,
		Body:       r.Body,
		Stored:     r.stored,
		SHA:        r.SHA,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
		Inline:     r.Kind == facts.ArrivesInline,
		CommentID:  r.commentID,
		Threads:    r.threads,
		ThreadsErr: r.threadsErr,
		Pending:    r.pending,
		Concluded:  r.concluded,
	}
}

func (rv *reviewer) react(ctx context.Context, r remoteReview, emoji string) (string, error) {
	if err := rv.pull.React(ctx, r.note(), emoji); err != nil {
		return "", err
	}

	return "reaction " + emoji, nil
}

func (rv *reviewer) patchBody(ctx context.Context, r remoteReview, body, what string) (string, error) {
	if err := rv.pull.EditBody(ctx, r.note(), body); err != nil {
		return "", err
	}

	return what, nil
}

func (rv *reviewer) resolveThreads(ctx context.Context, r remoteReview) (string, error) {
	if r.Kind != facts.ArrivesInline {
		return "", errors.New(
			"only an inline review has threads to resolve; declare reaction, marker or checkbox for a comment",
		)
	}

	if r.threadsErr != nil {
		return "", fmt.Errorf("reading the review's threads: %w", r.threadsErr)
	}

	if len(r.threads) == 0 {
		return "", errors.New("the review opened no threads to resolve")
	}

	if err := rv.pull.ResolveThreads(ctx, r.threads); err != nil {
		return "", err
	}

	return fmt.Sprintf("%d thread(s) resolved", len(r.threads)), nil
}
