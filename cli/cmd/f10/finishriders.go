package main

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ref"
)

// A bundle's riders at finish. The PR body carries a closing keyword per
// member, so a merge closes every one; a rider the PR delivers only in part
// has its keyword turned into a plain reference before the merge, or is
// reopened when the merge already happened. Which riders close is the
// user's call, asked once: finish refuses to guess.

// exitRiders is finish's exit when a bundle's riders have no --close
// answer yet: the finish skill asks which the PR delivers and reruns.
const exitRiders = 7

// closeAll and closeNone are the --close answers that name no rider.
const (
	closeAll  = "all"
	closeNone = "none"
)

// A ridersError is a bundle finished without --close: nothing changed.
type ridersError struct {
	task ref.Ref
}

func (e ridersError) Error() string {
	return fmt.Sprintf("%s ships with riders %s: pass --close with the riders this PR delivers "+
		"(ids, %s or %s); the rest stay open", e.task.ID, strings.Join(e.task.Riders(), ", "), closeAll, closeNone)
}

// closeChoice reads --close into the riders to close. A token names a
// rider in any form the tracker takes (`GH-2`, `2`, `#2`); one outside the
// bundle is a typo, refused.
func closeChoice(task ref.Ref, value string, resolve func(string) (ref.Ref, error)) ([]string, error) {
	riders := task.Riders()

	tokens := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })

	switch {
	case len(tokens) == 1 && strings.EqualFold(tokens[0], closeAll):
		return slices.Clone(riders), nil
	case len(tokens) == 1 && strings.EqualFold(tokens[0], closeNone):
		return []string{}, nil
	}

	closing := []string{}

	for _, tok := range tokens {
		r, err := resolve(tok)
		if err != nil {
			return nil, fmt.Errorf("--close %s: %w", tok, err)
		}

		if !slices.Contains(riders, r.ID) {
			return nil, fmt.Errorf("--close %s: not a rider of %s (%s)", r.ID, task.ID, strings.Join(riders, ", "))
		}

		if !slices.Contains(closing, r.ID) {
			closing = append(closing, r.ID)
		}
	}

	return closing, nil
}

// settleRiders makes the merge close only the chosen riders: before an
// open PR merges, each kept rider's closing keyword leaves its body; after
// a merge that already happened, each kept rider is reopened. A tracker
// that is not the host closes nothing on merge, so the notes say what to
// close by hand.
func settleRiders(ctx context.Context, in finishInput, branch string, merged bool) ([]string, error) {
	var kept, closing []ref.Ref

	for _, m := range in.task.MemberRefs()[1:] {
		if slices.Contains(in.close, m.ID) {
			closing = append(closing, m)
		} else {
			kept = append(kept, m)
		}
	}

	var notes []string

	if in.driver != nil {
		if len(closing) > 0 {
			notes = append(notes, "the tracker is not the host, so the merge closes no rider: close "+
				strings.Join(refIDs(closing), ", ")+" there")
		}

		return appendNote(notes, keptNote(kept)), nil
	}

	switch {
	case merged && len(kept) > 0:
		for _, m := range kept {
			if err := in.host.reopenIssue(ctx, m.Number); err != nil {
				return nil, fmt.Errorf("reopening %s, which the merge closed: %w", m.ID, err)
			}
		}

		notes = append(notes, "reopened "+strings.Join(refIDs(kept), ", ")+", which the merge had closed")
	case !merged:
		body, err := in.host.pullBody(ctx, branch)
		if err != nil {
			return nil, err
		}

		// a rerun after a failed merge may close a rider an earlier run kept
		edited := body
		for _, m := range kept {
			edited = keepOpen(edited, m.Number)
		}

		for _, m := range closing {
			edited = closeAgain(edited, m.Number)
		}

		if edited != body {
			if err := in.host.editPullBody(ctx, branch, edited); err != nil {
				return nil, fmt.Errorf("editing the closing keywords in the PR body: %w", err)
			}
		}
	}

	return appendNote(notes, keptNote(kept)), nil
}

func keptNote(kept []ref.Ref) string {
	if len(kept) == 0 {
		return ""
	}

	return "left open: " + strings.Join(refIDs(kept), ", ")
}

func refIDs(refs []ref.Ref) []string {
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.ID)
	}

	return ids
}

// closingKeyword is GitHub's and GitLab's closing keywords before an issue,
// by `#n` or by its url; the issue number goes in place of %s.
const closingKeyword = `(?i)\b(?:close[sd]?|closing|fix(?:e[sd])?|fixing|resolve[sd]?|resolving|` +
	`implement(?:s|ed|ing)?):?\s+(#%[1]s|https?://\S+/(?:issues|work_items)/%[1]s)\b`

// keepOpen turns every closing keyword for issue number into a plain
// reference, `Closes #12` into `Part of #12`, so the merge leaves it open.
func keepOpen(body, number string) string {
	re := regexp.MustCompile(fmt.Sprintf(closingKeyword, regexp.QuoteMeta(number)))

	return re.ReplaceAllString(body, "Part of $1")
}

// closeAgain undoes keepOpen for issue number: `Part of #12` back into
// `Closes #12`.
func closeAgain(body, number string) string {
	re := regexp.MustCompile(fmt.Sprintf(`\bPart of (#%[1]s|https?://\S+/(?:issues|work_items)/%[1]s)\b`,
		regexp.QuoteMeta(number)))

	return re.ReplaceAllString(body, "Closes $1")
}

// reopenIssue reopens issue number.
func (h host) reopenIssue(ctx context.Context, number string) error {
	_, err := h.run(ctx, "issue", "reopen", number)

	return err
}
