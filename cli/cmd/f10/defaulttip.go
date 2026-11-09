package main

import (
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// tipPolicy is how defaultTip answers when the local default branch and
// origin's copy both exist and neither holds the other, or, for originFirst,
// whenever origin's copy exists at all.
type tipPolicy int

const (
	// divergeFail refuses: a local landing would fold one side's commits into the other.
	divergeFail tipPolicy = iota
	// divergeOrigin takes origin's copy, which is what the host will merge onto.
	divergeOrigin
	// originFirst takes origin's copy whenever it exists, so unpushed local
	// commits never ride into a task branch.
	originFirst
)

// defaultTip is the default branch's tip to work from, and its name (def or
// origin/<def>). One side missing gives the other; otherwise a side that
// holds the other wins, and divergence goes by policy.
func defaultTip(ctx context.Context, dir, def string, policy tipPolicy) (string, string, error) {
	local, err := gitx.Ref(ctx, dir, "refs/heads/"+def)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", def, err)
	}

	remote, err := gitx.Ref(ctx, dir, "refs/remotes/origin/"+def)
	if err != nil {
		return "", "", fmt.Errorf("reading origin/%s: %w", def, err)
	}

	origin := "origin/" + def

	switch {
	case local == "" && remote == "":
		return "", "", fmt.Errorf("cannot read %s here, locally or on origin", def)
	case remote == "":
		return local, def, nil
	case local == "" || policy == originFirst:
		return remote, origin, nil
	case local == remote:
		return local, def, nil
	}

	if holds, err := gitx.IsAncestor(ctx, dir, remote, local); err != nil || holds {
		return local, def, err
	}

	// behind and diverged both read origin's copy unless divergence must fail
	if policy != divergeFail {
		return remote, origin, nil
	}

	if behind, err := gitx.IsAncestor(ctx, dir, local, remote); err != nil || behind {
		return remote, origin, err
	}

	return "", "", fmt.Errorf("%s and %s have diverged: bring %s up to date with origin first", def, origin, def)
}
