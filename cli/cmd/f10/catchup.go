package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/stack"
)

// The catchup engine, shared by drive and the watcher: git integrates, the
// binary verifies where it can, and the agent is prompted only for what git
// or verify could not settle. One record (drives.Catchup), one progress
// fingerprint and one leased push serve both callers.

// clearMarks unsets the catchup marks once the base is in and verify is
// green, and records the base the branch now stands on, as the step does.
func clearMarks(ctx context.Context, main, branch string, p stack.CatchupPlan) error {
	for _, key := range []branchmeta.Key{branchmeta.Landed, branchmeta.Moved} {
		if err := branchmeta.UnsetIfSet(ctx, main, branch, key); err != nil {
			return fmt.Errorf("clearing the %s mark on %s: %w", key, branch, err)
		}
	}

	if p.Found == stack.FoundAfter {
		return branchmeta.Set(ctx, main, branch, branchmeta.Tip, p.SHA)
	}

	return branchmeta.UnsetIfSet(ctx, main, branch, branchmeta.Tip)
}

// targetState is what keeps a catchup from going out for a PR's target:
// a stacked base not on origin yet (its unpushed commits would ride into
// the PR), a PR retargeted by hand (git's base would carry another
// branch's commits into it), or a local tip that is not origin's.
type targetState struct {
	unpushedBase, retargeted, diverged bool
}

// readTarget reads t against the PR's base and the branch's local and
// origin tips; a prBase of "" skips the retarget check.
func readTarget(ctx context.Context, main string, t stack.PRTarget, prBase, local, origin string) (targetState, error) {
	var s targetState

	if t.Key == branchmeta.Moved {
		on, err := gitx.IsAncestor(ctx, main, t.SHA, "refs/remotes/origin/"+t.Name)
		if err != nil {
			return s, fmt.Errorf("reading origin/%s: %w", t.Name, err)
		}

		s.unpushedBase = !on
	}

	s.retargeted = prBase != "" && prBase != strings.TrimPrefix(t.Name, "origin/")
	s.diverged = local != origin

	return s, nil
}

// errBaseNotPushed is a push held back because the base the catchup brought
// in is not on origin yet: the push would carry its commits into the PR.
var errBaseNotPushed = errors.New("its base is not on origin yet")

// originMovedError refuses a force-push that could drop commits pushed to
// the branch while its catchup ran: origin moved, or, unseen, nothing
// recorded what origin held when the catchup went out.
type originMovedError struct {
	branch, base, path string
	unseen             bool
}

func (e originMovedError) Error() string {
	if e.unseen {
		return fmt.Sprintf("%s caught up with %s, not pushed: the drive did not read origin/%s when the catchup "+
			"went out, so a force-push could drop commits pushed since", e.branch, e.base, e.branch)
	}

	return fmt.Sprintf("%s caught up with %s, and origin/%s moved meanwhile: not pushed", e.branch, e.base, e.branch)
}

// pushCatchup is the one push after a clean catchup of a branch origin has:
// plain when it fast-forwards origin, leased against rec's origin tip after
// a rewrite, and never carrying a base origin lacks. A rewrite with no live
// lease is refused as unseen; a push or a refusal spends the lease.
func pushCatchup(ctx context.Context, dir, branch string, rec *drives.Catchup) (string, error) {
	tracking := "refs/remotes/origin/" + branch

	pushed, err := gitx.Ref(ctx, dir, tracking)
	if err != nil || pushed == "" {
		return "", err
	}

	// fetched first, so a push made while the catchup ran shows here
	if _, err := gitx.Exec(ctx, dir, "fetch", "origin"); err != nil {
		return "", err
	}

	if pushed, err = gitx.Ref(ctx, dir, tracking); err != nil {
		return "", err
	}

	head, err := gitx.Ref(ctx, dir, "HEAD")
	if err != nil || head == pushed {
		return "", err
	}

	base := "its base"
	if rec != nil && rec.Base != "" {
		base = rec.Base
	}

	if rec != nil && rec.TargetRef != "" && rec.Target != "" {
		seen, err := gitx.IsAncestor(ctx, dir, rec.Target, rec.TargetRef)
		if err != nil {
			return "", err
		}

		if !seen {
			return "", errBaseNotPushed
		}
	}

	refused := originMovedError{branch: branch, base: base, path: dir}

	fastForward, err := gitx.IsAncestor(ctx, dir, pushed, "HEAD")
	if err != nil {
		return "", err
	}

	// a fast-forward drops nothing, so only a rewrite needs the lease, which
	// a refusal spends: a rerun must not replay a lease origin already broke
	switch {
	case fastForward:
	case rec == nil || rec.Spent:
		refused.unseen = true

		return "", refused
	case pushed != rec.Origin:
		rec.Spent = true

		return "", refused
	}

	lease, how := "", "pushed"
	if !fastForward {
		lease, how = pushed, "pushed with --force-with-lease (the catchup rewrote it)"
	}

	if err := gitx.Push(ctx, dir, branch, lease); err != nil {
		return "", fmt.Errorf("pushing %s, caught up with %s: %w", branch, base, err)
	}

	// origin now holds this push, so the record leases nothing further
	if rec != nil {
		rec.Spent = true
	}

	return how, nil
}
