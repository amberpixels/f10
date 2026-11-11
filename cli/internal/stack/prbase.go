package stack

import (
	"context"
	"fmt"
	"io"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// PRBase is the branch the subject's request targets or would target. A
// recorded base found nowhere was finished and deleted, so its code is on
// the default branch; that is said on warn. PRTargetOf keeps "found nowhere"
// as its own answer, which drive and the watcher wait on.
func PRBase(ctx context.Context, warn io.Writer, main, branch string) (string, error) {
	after, sha, err := RecordedBase(ctx, main, branch)
	if err != nil {
		return "", err
	}

	if after != "" && sha != "" {
		return after, nil
	}

	def, err := LocalDefault(ctx, main)
	if err != nil {
		return "", err
	}

	if after != "" {
		ui.Warnf(warn, "the recorded base %s is gone locally and on origin, so the base is %s", after, def)
	}

	return def, nil
}

// ForkPoint is where the subject's branch left its base: the newer of the
// merge bases with the local and origin copies, so neither unpushed commits
// of the base nor origin moving ahead read as this change.
func ForkPoint(ctx context.Context, dir, head, base string) (string, error) {
	point, err := NewerMergeBase(ctx, dir, head, base)
	if err == nil && point == "" {
		err = fmt.Errorf("%s shares no history with %s, locally or on origin", head, base)
	}

	return point, err
}

// NewerMergeBase is the newer of head's merge bases with base's local and
// origin copies, "" with neither. Two that diverged give origin's, the copy a
// request is compared against and a guard integrates.
func NewerMergeBase(ctx context.Context, dir, head, base string) (string, error) {
	var tip string

	for _, r := range []string{"refs/heads/" + base, "refs/remotes/origin/" + base} {
		mb, err := gitx.MergeBase(ctx, dir, head, r)

		switch {
		case gitx.UnknownObject(err):
			continue // that copy does not exist
		case err != nil:
			return "", err
		case mb == "" || mb == tip:
			continue
		}

		if tip != "" {
			older, err := gitx.IsAncestor(ctx, dir, mb, tip)
			if err != nil {
				return "", err
			}

			if older {
				continue
			}
		}

		tip = mb
	}

	return tip, nil
}
