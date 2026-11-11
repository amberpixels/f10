package worktree

import (
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/stack"
)

// RecordTip records the base commit branch is built on, so a catchup after
// the base's history is rewritten replays only branch's own commits. The
// newer of the local and origin merge-bases wins: a guard integrates origin's
// copy, which a stale local branch would hide. A base found nowhere keeps the
// tip recorded before.
func RecordTip(ctx context.Context, dir, branch, base string) error {
	tip, err := stack.NewerMergeBase(ctx, dir, "refs/heads/"+branch, base)
	if err != nil || tip == "" {
		return err
	}

	if err := branchmeta.Set(ctx, dir, branch, branchmeta.Tip, tip); err != nil {
		return fmt.Errorf("recording %s's base tip on %s: %w", branch, base, err)
	}

	return nil
}
