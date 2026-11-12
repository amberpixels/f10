package stack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// SetMark writes the mark a guard printed (`<key> <value>`) on the branch dir
// works on, and returns it. Resolving the catchup again would name no mark: a
// stopped rebase leaves HEAD on the base, which reads as already in.
func SetMark(ctx context.Context, dir, main, mark string) (string, error) {
	name, value, _ := strings.Cut(strings.TrimSpace(mark), " ")

	key, value := branchmeta.Key(name), strings.TrimSpace(value)
	if key != branchmeta.Landed && key != branchmeta.Moved || value == "" {
		return "", fmt.Errorf("--mark %q: want `%s <branch>` or `%s <sha>`, as the mark row prints it",
			mark, branchmeta.Landed, branchmeta.Moved)
	}

	branch, err := gitx.WorkedBranch(ctx, dir)
	if err != nil {
		return "", err
	}

	if branch == "" {
		return "", fmt.Errorf("no branch to mark in %s: HEAD is detached and no rebase is in progress", dir)
	}

	return branch, branchmeta.Set(ctx, main, branch, key, value)
}

// ClearMarks unsets the catchup marks once the base is in and verify is
// green, and records the base the branch now stands on, as the step does.
func ClearMarks(ctx context.Context, main, branch string, p CatchupPlan) error {
	for _, key := range []branchmeta.Key{branchmeta.Landed, branchmeta.Moved} {
		if err := branchmeta.UnsetIfSet(ctx, main, branch, key); err != nil {
			return fmt.Errorf("clearing the %s mark on %s: %w", key, branch, err)
		}
	}

	if p.Found == FoundAfter {
		return branchmeta.Set(ctx, main, branch, branchmeta.Tip, p.SHA)
	}

	return branchmeta.UnsetIfSet(ctx, main, branch, branchmeta.Tip)
}

// targetState is what keeps a catchup from going out for a PR's target:
// a stacked base not on origin yet (its unpushed commits would ride into
// the PR), a PR retargeted by hand (git's base would carry another
// branch's commits into it), or a local tip that is not origin's.
type targetState struct {
	// UnpushedBase is a stacked base read from its local branch only. The
	// drive integrates it and holds the push (ErrBaseNotPushed), because the
	// dependents it runs build on that unpushed code on purpose. The watcher
	// declines, because nobody asked it to build on code not yet pushed.
	UnpushedBase bool

	Retargeted, Diverged bool
}

// ReadTarget reads t against the PR's base and the branch's local and
// origin tips; a prBase of "" skips the retarget check.
func ReadTarget(ctx context.Context, main string, t PRTarget, prBase, local, origin string) (targetState, error) {
	var s targetState

	if t.Key == branchmeta.Moved {
		on, err := gitx.IsAncestor(ctx, main, t.SHA, "refs/remotes/origin/"+t.Name)
		if err != nil {
			return s, fmt.Errorf("reading origin/%s: %w", t.Name, err)
		}

		s.UnpushedBase = !on
	}

	s.Retargeted = prBase != "" && prBase != strings.TrimPrefix(t.Name, "origin/")
	s.Diverged = local != origin

	return s, nil
}

// ErrBaseNotPushed is a push held back because the base the catchup brought
// in is not on origin yet: the push would carry its commits into the PR.
var ErrBaseNotPushed = errors.New("its base is not on origin yet")

// OriginMovedError refuses a force-push that could drop commits pushed to
// the branch while its catchup ran: origin moved, or, unseen, nothing
// recorded what origin held when the catchup went out.
type OriginMovedError struct {
	Branch, base, Path string
	Unseen             bool
}

func (e OriginMovedError) Error() string {
	if e.Unseen {
		return fmt.Sprintf("%s caught up with %s, not pushed: the drive did not read origin/%s when the catchup "+
			"went out, so a force-push could drop commits pushed since", e.Branch, e.base, e.Branch)
	}

	return fmt.Sprintf("%s caught up with %s, and origin/%s moved meanwhile: not pushed", e.Branch, e.base, e.Branch)
}

// PushCatchup is the one push after a clean catchup of a branch origin has:
// plain when it fast-forwards origin, leased against rec's origin tip after
// a rewrite, and never carrying a base origin lacks. A rewrite with no live
// lease is refused as unseen; a push or a refusal spends the lease.
func PushCatchup(ctx context.Context, dir, branch string, rec *drives.Catchup) (string, error) {
	tracking := "refs/remotes/origin/" + branch

	pushed, err := gitx.Ref(ctx, dir, tracking)
	if err != nil || pushed == "" {
		return "", err
	}

	// fetched fresh, not through the tick's FetchOnce, so a push made while
	// the catchup ran shows here
	if err := gitx.Fetch(ctx, dir); err != nil {
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
			return "", ErrBaseNotPushed
		}
	}

	refused := OriginMovedError{Branch: branch, base: base, Path: dir}

	fastForward, err := gitx.IsAncestor(ctx, dir, pushed, "HEAD")
	if err != nil {
		return "", err
	}

	// a fast-forward drops nothing, so only a rewrite needs the lease, which
	// a refusal spends: a rerun must not replay a lease origin already broke
	switch {
	case fastForward:
	case rec == nil || rec.Spent:
		refused.Unseen = true

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
