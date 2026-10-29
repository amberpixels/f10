package main

import (
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A catchup as seen from outside the agent that runs it, shared by drive
// and the watcher.

// prTarget is what a task's PR targets and the mark that sends a catchup for
// it: origin's copy of the f10-after-branch base, else its local copy, else
// the default branch. sha is "" for a base found nowhere.
type prTarget struct {
	sha, name, key, value string
}

func prTargetOf(ctx context.Context, main, branch string) (prTarget, error) {
	after, sha, err := recordedBase(ctx, main, branch)
	if err != nil {
		return prTarget{}, err
	}

	if after != "" {
		return prTarget{sha: sha, name: after, key: cfgMoved, value: sha}, nil
	}

	// the default branch resolves as the catchup will read it, so the mark
	// and the catchup never name different commits
	sha, name, err := defaultTarget(ctx, main)
	if err != nil {
		return prTarget{}, err
	}

	def, err := localDefault(ctx, main)

	return prTarget{sha: sha, name: name, key: cfgLanded, value: def}, err
}

// recordedBase is the f10-after-branch base start recorded on branch, and
// its sha, origin's copy first; sha is "" for a base found nowhere.
func recordedBase(ctx context.Context, main, branch string) (string, string, error) {
	after, err := gitx.Config(ctx, main, "branch."+branch+"."+cfgAfterBranch)
	if err != nil || after == "" {
		return "", "", err
	}

	for _, ref := range []string{"refs/remotes/origin/" + after, "refs/heads/" + after} {
		sha, err := gitx.Ref(ctx, main, ref)
		if err != nil || sha != "" {
			return after, sha, err
		}
	}

	return after, "", nil
}

// defaultTarget is the default branch's sha as the catchup step reads it for
// f10-landed: the local branch when it holds origin's, else origin's.
func defaultTarget(ctx context.Context, main string) (string, string, error) {
	def, err := localDefault(ctx, main)
	if err != nil {
		return "", "", err
	}

	local, err := gitx.Ref(ctx, main, "refs/heads/"+def)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", def, err)
	}

	remote, err := gitx.Ref(ctx, main, "refs/remotes/origin/"+def)
	if err != nil {
		return "", "", fmt.Errorf("reading origin/%s: %w", def, err)
	}

	switch {
	case local == "" && remote == "":
		return "", "", fmt.Errorf("cannot read %s here", def)
	case remote == "":
		return local, def, nil
	case local == "":
		return remote, "origin/" + def, nil
	}

	holds, err := gitx.IsAncestor(ctx, main, remote, local)

	switch {
	case err != nil:
		return "", "", err
	case holds:
		return local, def, nil
	}

	return remote, "origin/" + def, nil
}

func markOn(ctx context.Context, main, branch string) (string, error) {
	for _, key := range []string{cfgLanded, cfgMoved} {
		v, err := gitx.Config(ctx, main, "branch."+branch+"."+key)
		if err != nil {
			return "", fmt.Errorf("reading the %s mark on %s: %w", key, branch, err)
		}

		if v != "" {
			return key, nil
		}
	}

	return "", nil
}

// branchUnsettled says why a catchup did not bring branch up to date, or "".
// Read from git, since catchup clears the mark only once verify is green.
func branchUnsettled(ctx context.Context, main, dir, branch string) string {
	key, err := markOn(ctx, main, branch)

	switch {
	case err != nil:
		return err.Error()
	case key != "":
		return "the branch is still marked " + key
	}

	return treeBusy(ctx, dir)
}

func treeBusy(ctx context.Context, dir string) string {
	for _, head := range []struct{ ref, what string }{{"MERGE_HEAD", "merge"}, {"REBASE_HEAD", "rebase"}} {
		sha, err := gitx.Ref(ctx, dir, head.ref)

		switch {
		case err != nil:
			return fmt.Sprintf("could not tell whether a %s is in progress in %s: %v", head.what, dir, err)
		case sha != "":
			return fmt.Sprintf("a %s is still in progress in %s", head.what, dir)
		}
	}

	dirty, err := gitx.Status(ctx, dir)

	switch {
	case err != nil:
		return err.Error()
	case len(dirty) > 0:
		return fmt.Sprintf("%d uncommitted paths in %s", len(dirty), dir)
	}

	return ""
}

// pushCaughtUp pushes plain when the catchup merged, so origin's copy is an
// ancestor, and with a lease against the pushed sha when it rebased.
func pushCaughtUp(ctx context.Context, dir, branch, pushed, base string) (string, error) {
	fastForward, err := gitx.IsAncestor(ctx, dir, pushed, "HEAD")
	if err != nil {
		return "", err
	}

	lease, how := "", "pushed"
	if !fastForward {
		lease, how = pushed, "pushed with --force-with-lease (the catchup rewrote it)"
	}

	if err := gitx.Push(ctx, dir, branch, lease); err != nil {
		return "", fmt.Errorf("pushing %s, caught up with %s: %w", branch, base, err)
	}

	return how, nil
}
