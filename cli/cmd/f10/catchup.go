package main

import (
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A catchup as seen from outside the agent that runs it, shared by f10
// drive and the watcher: what a branch's pull request targets, whether a
// catchup ended clean, and the push after one.

// prTarget is what a task's pull request targets and the mark that sends
// a catchup for it: origin's copy of the branch f10-after-branch records,
// which the stacked PR targets, else its local copy, else the default
// branch as defaultTarget resolves it. sha is "" for a base found nowhere.
type prTarget struct {
	sha, name, key, value string
}

func prTargetOf(ctx context.Context, main, branch string) (prTarget, error) {
	after, err := gitx.Config(ctx, main, "branch."+branch+"."+cfgAfterBranch)
	if err != nil {
		return prTarget{}, err
	}

	if after != "" {
		sha := ""

		for _, ref := range []string{"refs/remotes/origin/" + after, "refs/heads/" + after} {
			if sha, err = gitx.Ref(ctx, main, ref); err != nil || sha != "" {
				break
			}
		}

		return prTarget{sha: sha, name: after, key: cfgMoved, value: sha}, err
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

// defaultTarget is the default branch's sha as the catchup step reads it
// for f10-landed, and its name for prose: the local branch when it holds
// origin's, else origin's, else the local one.
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

// markOn is the catchup mark a branch carries, f10-landed before
// f10-base-moved, or "" for none.
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

// branchUnsettled says why a catchup of branch, checked out at dir, did not
// bring it up to date, or "" when it did. Catchup owns no phase, so its
// success is read from git: the mark gone, which catchup clears only once
// verify is green, no merge or rebase left open, and a clean tree.
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

// treeBusy says why the checkout at dir cannot take a catchup, or "": a
// merge or rebase in progress, or uncommitted paths.
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

// pushCaughtUp pushes the caught-up branch at dir over origin's copy at
// pushed: plain when the catchup merged, so origin's copy is an ancestor,
// with a lease against that sha when it rebased. It returns how the push
// went, for prose; base names the target in the error.
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
