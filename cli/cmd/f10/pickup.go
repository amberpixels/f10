package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// Picking up someone else's PR: nothing is local, so start checks the branch
// out tracking origin's and links it to its task in local config, since the
// branch's name need not carry the id. A merged or closed request has no
// branch worth tracking: its worktree is made at the commit it landed as, on
// a branch named for the task, for a post-merge review.

// pickupTask prefers a closing keyword's issue (this repository's only) on a
// host-issues tracker, else an id in the body, title or branch, in that order.
func pickupTask(prefix string, hostIssues bool, p forge.PR) string {
	if hostIssues && prefix != "" {
		repo := p.URL
		if i := worktree.PullPathRE.FindStringIndex(repo); i != nil {
			repo = strings.TrimSuffix(repo[:i[0]], "/-")
		}

		for _, m := range closingRE.FindAllStringSubmatch(p.Body, -1) {
			issue := m[1]

			if !strings.HasPrefix(issue, "#") {
				rest, ok := strings.CutPrefix(issue, repo+"/")
				if !ok || strings.Count(strings.TrimPrefix(rest, "-/"), "/") != 1 {
					continue
				}

				issue = issue[strings.LastIndexByte(issue, '/')+1:]
			}

			return prefix + "-" + strings.TrimPrefix(issue, "#")
		}
	}

	for _, text := range []string{p.Body, p.Title, p.Head} {
		if id := ref.Match(prefix, text); id != "" {
			return id
		}
	}

	return ""
}

func refusePull(p forge.PR) error {
	switch {
	case p.Fork:
		return fmt.Errorf("%s comes from a fork: PRs from forks are not supported yet", p.URL)
	case p.Status == forge.Open && p.Head == "":
		return fmt.Errorf("%s has no head branch in the host's answer", p.URL)
	case p.Status == forge.Merged && p.Landed == "":
		return fmt.Errorf("%s is merged, but the host names no merge commit to check out", p.URL)
	case p.Status == forge.Closed && p.HeadSHA == "":
		return fmt.Errorf("%s is closed, but the host names no head commit to check out", p.URL)
	case p.Status != forge.Open && p.Status != forge.Merged && p.Status != forge.Closed:
		return fmt.Errorf("%s is %q: start picks up open, merged and closed PRs", p.URL, p.Status)
	}

	return nil
}

// syncPickup fast-forwards a local branch start found only when it is behind
// and checked out nowhere; otherwise a note says how it differs.
func syncPickup(ctx context.Context, main, branch string, checkedOut bool) (string, error) {
	local, remote := "refs/heads/"+branch, "refs/remotes/origin/"+branch

	upToDate, err := gitx.IsAncestor(ctx, main, remote, local)
	if err != nil {
		return "", err
	}

	behind, err := gitx.IsAncestor(ctx, main, local, remote)
	if err != nil {
		return "", err
	}

	switch {
	case upToDate:
		return "", nil
	case !behind:
		return fmt.Sprintf("%s has diverged from origin/%s: the worktree holds the local commits", branch, branch), nil
	case checkedOut:
		return fmt.Sprintf("%s is behind origin/%s: pull in its worktree", branch, branch), nil
	}

	if err := gitx.TrackBranch(ctx, main, branch, true); err != nil {
		return "", err
	}

	return fmt.Sprintf("fast-forwarded %s to origin/%s", branch, branch), nil
}

// fetchPickup: a handover has not fetched the request's branch yet. A landed
// request's commit must be here before its worktree is made, and a closed
// request's head comes only through the host's own ref for it.
func fetchPickup(ctx context.Context, main string, pick *hostops.Pickup) error {
	if err := gitx.Fetch(ctx, main); err != nil {
		return fmt.Errorf("fetching %s for %s: %w", pick.Branch, pick.URL, err)
	}

	if pick.HeadRef != "" {
		if err := gitx.FetchRef(ctx, main, pick.HeadRef); err != nil {
			return fmt.Errorf("fetching %s for %s: %w", pick.HeadRef, pick.URL, err)
		}
	}

	if pick.Landed() && stack.RefSHA(ctx, main, pick.SHA+"^{commit}") == "" {
		return fmt.Errorf("%s is %s at %s, which origin does not hold after a fetch", pick.URL, pick.State, pick.SHA)
	}

	return nil
}
