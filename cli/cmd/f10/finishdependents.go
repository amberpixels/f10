package main

import (
	"cmp"
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// releaseDependents clears other branches' `start --after` dependency on the
// finished one and marks them landed: their pr must target the default
// branch now, and their next ship or catchup must bring it in first. merged
// says the finished branch's code reached the default branch.
func releaseDependents(ctx context.Context, dir, branch, def string, merged bool) ([]string, error) {
	records, err := branchmeta.LoadAll(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("reading which branches depend on %s: %w", branch, err)
	}

	var notes []string

	for _, dependent := range branchmeta.Dependents(records, branch) {
		base, err := branchmeta.Get(ctx, dir, dependent, branchmeta.After)
		if err != nil {
			return nil, fmt.Errorf("releasing %s from its dependency on %s: %w", dependent, branch, err)
		}

		if err := releaseTip(ctx, dir, dependent, branch, merged); err != nil {
			return nil, err
		}

		for _, key := range []branchmeta.Key{branchmeta.After, branchmeta.AfterBranch} {
			if err := branchmeta.Unset(ctx, dir, dependent, key); err != nil {
				return nil, fmt.Errorf("releasing %s from its dependency on %s: %w", dependent, branch, err)
			}
		}

		if err := branchmeta.Set(ctx, dir, dependent, branchmeta.Landed, cmp.Or(base, branch)); err != nil {
			return nil, fmt.Errorf("marking %s landed on %s: %w", dependent, def, err)
		}

		notes = append(notes, fmt.Sprintf("%s depended on %s: the dependency is cleared and its pr targets %s now; "+
			"/f10:catchup, or its next ship unless catchup is off, brings %s in first", dependent, branch, def, def))
	}

	return notes, nil
}

// releaseTip records the dependent's base tip before the branch is deleted,
// the last time it can be read. A base that never merged (a closed spike)
// leaves none: an onto from it would drop the base's code from the dependent.
func releaseTip(ctx context.Context, dir, dependent, branch string, merged bool) error {
	if merged {
		return worktree.RecordTip(ctx, dir, dependent, branch)
	}

	tip, err := branchmeta.Get(ctx, dir, dependent, branchmeta.Tip)
	if err != nil || tip == "" {
		return err
	}

	if err := branchmeta.Unset(ctx, dir, dependent, branchmeta.Tip); err != nil {
		return fmt.Errorf("clearing %s's base tip on %s: %w", dependent, branch, err)
	}

	return nil
}
