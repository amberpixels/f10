package finish

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/textx"
)

// A bundle's riders at finish. The PR body says which riders the PR
// delivers: `Closes #n` closes one on merge, `Part of #n` leaves it open.
// The drive and the watcher follow the body; a manual finish asks once,
// offering the body's choice, and --close overrides it.

const (
	closeAll  = "all"
	closeNone = "none"
)

// A RidersError is a bundle finished without --close: nothing changed.
type RidersError struct {
	task ref.Ref
	body []string // the riders the PR body closes
	read bool     // a PR body was read; false for a branch never pushed
}

func (e RidersError) Error() string {
	msg := fmt.Sprintf("%s ships with riders %s: pass --close with the riders this PR delivers "+
		"(ids, %s or %s); the rest stay open", e.task.ID, strings.Join(e.task.Riders(), ", "), closeAll, closeNone)

	switch {
	case !e.read:
		return msg
	case len(e.body) == 0:
		return msg + "; the PR body closes none of them: --close " + closeNone + " follows it"
	default:
		return msg + "; the PR body closes " + strings.Join(e.body, ", ") +
			": --close " + strings.Join(e.body, ",") + " follows it"
	}
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
// merge by editing the body's keywords, after one by reopening those the
// body closed. A tracker that is not the host closes nothing, so the notes
// say what to close by hand.
func settleRiders(
	ctx context.Context,
	in FinishInput,
	branch string,
	merged bool,
	mergedBody string,
) ([]string, error) {
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
	case merged:
		// a rider under `Part of` was never closed, so only the body's closes reopen
		closed := BodyCloses(mergedBody, kept)
		for _, m := range kept {
			if !slices.Contains(closed, m.ID) {
				continue
			}

			if err := in.Host.SetIssueOpen(ctx, m.Number, true); err != nil {
				return nil, fmt.Errorf("reopening %s, which the merge closed: %w", m.ID, err)
			}
		}

		if len(closed) > 0 {
			notes = append(notes, "reopened "+strings.Join(closed, ", ")+", which the merge had closed")
		}
	default:
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
