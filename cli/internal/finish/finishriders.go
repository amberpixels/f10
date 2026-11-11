package finish

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/textx"
)

// A bundle's riders at finish. The PR body carries a closing keyword per
// member, so a merge closes every one; which riders the PR really delivers
// is the user's call, asked once: finish refuses to guess.

const (
	closeAll  = "all"
	closeNone = "none"
)

// A RidersError is a bundle finished without --close: nothing changed.
type RidersError struct {
	task ref.Ref
}

func (e RidersError) Error() string {
	return fmt.Sprintf("%s ships with riders %s: pass --close with the riders this PR delivers "+
		"(ids, %s or %s); the rest stay open", e.task.ID, strings.Join(e.task.Riders(), ", "), closeAll, closeNone)
}

// CloseChoice reads --close; a token outside the bundle is a typo, refused.
func CloseChoice(task ref.Ref, value string, resolve func(string) (ref.Ref, error)) ([]string, error) {
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

// settleRiders makes the merge close only the chosen riders: before the
// merge by editing the body's keywords, after one by reopening. A tracker
// that is not the host closes nothing, so the notes say what to close by hand.
func settleRiders(ctx context.Context, in FinishInput, branch string, merged bool) ([]string, error) {
	var kept, closing []ref.Ref

	for _, m := range in.Task.MemberRefs()[1:] {
		if slices.Contains(in.Close, m.ID) {
			closing = append(closing, m)
		} else {
			kept = append(kept, m)
		}
	}

	var notes []string

	if !in.HostCloses {
		if len(closing) > 0 {
			notes = append(notes, "the tracker is not the host, so the merge closes no rider: close "+
				strings.Join(refIDs(closing), ", ")+" there")
		}

		return textx.AppendNote(notes, keptNote(kept)), nil
	}

	switch {
	case merged && len(kept) > 0:
		for _, m := range kept {
			if err := in.Host.SetIssueOpen(ctx, m.Number, true); err != nil {
				return nil, fmt.Errorf("reopening %s, which the merge closed: %w", m.ID, err)
			}
		}

		notes = append(notes, "reopened "+strings.Join(refIDs(kept), ", ")+", which the merge had closed")
	case !merged:
		body, err := in.Host.PRBody(ctx, branch)
		if err != nil {
			return nil, err
		}

		// a rerun after a failed merge may close a rider an earlier run kept
		edited := body
		for _, m := range kept {
			edited = KeepOpen(edited, m.Number)
		}

		for _, m := range closing {
			edited = CloseAgain(edited, m.Number)
		}

		if edited != body {
			if err := in.Host.EditPull(ctx, branch, "", edited); err != nil {
				return nil, fmt.Errorf("editing the closing keywords in the PR body: %w", err)
			}
		}
	}

	return textx.AppendNote(notes, keptNote(kept)), nil
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
