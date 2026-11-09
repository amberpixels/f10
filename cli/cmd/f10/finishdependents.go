package main

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// releaseDependents clears other branches' `start --after` dependency on the
// finished one and marks them landed: their pr must target the default
// branch now, and their next ship or catchup must bring it in first. merged
// says the finished branch's code reached the default branch.
func releaseDependents(ctx context.Context, dir, branch, def string, merged bool) ([]string, error) {
	entries, err := gitx.ConfigEntries(ctx, dir, `^branch\..*\.`+cfgAfterBranch+`$`)
	if err != nil {
		return nil, fmt.Errorf("reading which branches depend on %s: %w", branch, err)
	}

	var notes []string

	for _, e := range entries {
		if e.Value != branch {
			continue
		}

		dependent := strings.TrimSuffix(strings.TrimPrefix(e.Key, "branch."), "."+cfgAfterBranch)
		section := "branch." + dependent + "."

		base, err := gitx.Config(ctx, dir, section+cfgAfter)
		if err != nil {
			return nil, fmt.Errorf("releasing %s from its dependency on %s: %w", dependent, branch, err)
		}

		if err := releaseTip(ctx, dir, dependent, branch, merged); err != nil {
			return nil, err
		}

		for _, key := range []string{cfgAfter, cfgAfterBranch} {
			if err := gitx.UnsetConfig(ctx, dir, section+key); err != nil {
				return nil, fmt.Errorf("releasing %s from its dependency on %s: %w", dependent, branch, err)
			}
		}

		if err := gitx.SetConfig(ctx, dir, section+cfgLanded, cmp.Or(base, branch)); err != nil {
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
		return recordTip(ctx, dir, dependent, branch)
	}

	key := "branch." + dependent + "." + cfgTip

	tip, err := gitx.Config(ctx, dir, key)
	if err != nil || tip == "" {
		return err
	}

	if err := gitx.UnsetConfig(ctx, dir, key); err != nil {
		return fmt.Errorf("clearing %s's base tip on %s: %w", dependent, branch, err)
	}

	return nil
}
