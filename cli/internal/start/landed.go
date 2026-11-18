package start

import (
	"context"
	"fmt"
	"strconv"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
)

// A LandedLookup is the newest request whose head is branch, when it is
// merged or closed, and false when there is none or the newest is open.
type LandedLookup func(ctx context.Context, branch string) (forge.PR, bool, error)

// LandedVia asks the host's requests by head branch, newest first: an open
// one means the branch is in use, whatever landed on it before.
func LandedVia(h forge.Host) LandedLookup {
	return func(ctx context.Context, branch string) (forge.PR, bool, error) {
		prs, err := h.PullsByHead(ctx, branch)
		if err != nil || len(prs) == 0 {
			return forge.PR{}, false, err
		}

		p := prs[0]

		return p, p.Status == forge.Merged || p.Status == forge.Closed, nil
	}
}

// LandedFor is LandedVia on the target's host, nil with no host CLI, which
// skips the check: start --plan runs without one.
func LandedFor(t *target.Target) LandedLookup {
	h, err := hostops.HostOf(t)
	if err != nil {
		return nil
	}

	return LandedVia(h.Host)
}

// landedMax bounds the numbered names tried, so a host answering "landed"
// to everything ends in an error, not a loop.
const landedMax = 20

// avoidLanded keeps a new branch off a name a landed request used as its
// head. Finish, the watcher and the drive look requests up by head branch,
// so each would read that request as this branch's and finish the task.
func avoidLanded(
	ctx context.Context,
	in StartInput,
	name string,
	existing stack.FoundBranch,
) (string, stack.FoundBranch, string, error) {
	if in.Landed == nil {
		return name, existing, "", nil
	}

	if existing.Name != "" {
		stale, p, err := staleLanded(ctx, in, existing)
		if err != nil || !stale {
			return name, existing, "", err
		}

		if !existing.Remote {
			return "", existing, "", fmt.Errorf(
				"%s is %s's branch, which merged, and carries nothing past it: "+
					"remove it (git branch -D %s, after removing any worktree that has it) and start again",
				existing.Name, p.URL, existing.Name)
		}

		// origin's copy of a landed head is stale: a new name is made below
		name, existing = existing.Name, stack.FoundBranch{}
	}

	took := "" // the url of the request that landed on name itself

	for i := 1; i <= landedMax; i++ {
		try := name
		if i > 1 {
			try = name + "-" + strconv.Itoa(i)

			if stack.RefSHA(ctx, in.Main, "refs/heads/"+try) != "" ||
				stack.RefSHA(ctx, in.Main, "refs/remotes/origin/"+try) != "" {
				continue
			}
		}

		p, landed, err := in.Landed(ctx, try)
		if err != nil {
			return "", existing, "", fmt.Errorf("reading the requests whose head is %s: %w", try, err)
		}

		if !landed {
			if i == 1 {
				return try, existing, "", nil
			}

			return try, existing, fmt.Sprintf("%s was %s's branch, which landed: the next PR's branch is %s",
				name, took, try), nil
		}

		if i == 1 {
			took = p.URL
		}
	}

	return "", existing, "", fmt.Errorf("%s and %s-2 to -%d are all heads of landed requests: pass a suffix",
		name, name, landedMax)
}

// staleLanded says a found branch is a merged request's head with nothing
// past it: its tip is that head, or an ancestor of it. A closed request's
// head is in no default branch, so its branch is the only copy and is reused.
func staleLanded(ctx context.Context, in StartInput, b stack.FoundBranch) (bool, forge.PR, error) {
	p, landed, err := in.Landed(ctx, b.Name)
	if err != nil {
		return false, p, fmt.Errorf("reading the requests whose head is %s: %w", b.Name, err)
	}

	if !landed || p.Status != forge.Merged || p.HeadSHA == "" {
		return false, p, nil
	}

	r := "refs/heads/" + b.Name
	if b.Remote {
		r = "refs/remotes/origin/" + b.Name
	}

	tip := stack.RefSHA(ctx, in.Main, r)
	if tip == "" {
		return false, p, nil
	}

	if tip == p.HeadSHA {
		return true, p, nil
	}

	behind, err := gitx.IsAncestor(ctx, in.Main, tip, p.HeadSHA)

	return err == nil && behind, p, nil
}
