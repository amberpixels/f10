package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// Picking up someone else's PR: nothing is local, so start checks the branch
// out tracking origin's and links it to its task in local config, since the
// branch's name need not carry the id. A merged or closed request has no
// branch worth tracking: its worktree is made at the commit it landed as, on
// a branch named for the task, for a post-merge review.

// exitNoTask: the start skill asks for the id and reruns with --task.
const exitNoTask = 3

type pickup struct {
	number string
	url    string
	branch string // the request's head, as the host names it
	view   string // the f10 command that reads the request with its comments
	diff   string
	// sha is where a merged or closed request is checked out, "" for an open
	// one; state says which it is, for the prompt and the report; headRef is
	// the host's ref for a closed request's head, which no branch holds
	sha     string
	state   string
	headRef string
}

// landed reports a request that merged or closed, checked out at sha.
func (p *pickup) landed() bool { return p.sha != "" }

// pullPathRE matches GitHub's `/pull/12` and GitLab's `/-/merge_requests/12`.
var pullPathRE = regexp.MustCompile(`/(?:pull|merge_requests)/(\d+)(?:[/?#]|$)`)

// pullToken's probe is true for a bare `#12`, which may name a task instead:
// ask the host before taking it (GitHub's issues and PRs share one counter).
func pullToken(token string) (string, bool) {
	switch {
	case strings.Contains(token, "://"):
		if m := pullPathRE.FindStringSubmatch(token); m != nil {
			return m[1], false
		}
	case len(token) > 1 && (token[0] == '!' || token[0] == '#') && digits(token[1:]):
		return token[1:], token[0] == '#'
	}

	return "", false
}

func digits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// closingRE reads `Closes #12` and `fixes: https://…/issues/12` as both hosts do.
var closingRE = regexp.MustCompile(
	`(?i)(?:^|[^a-z0-9])(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?\s+(#\d+|https?://\S+?/issues/\d+)`)

// pickupTask prefers a closing keyword's issue (this repository's only) on a
// host-issues tracker, else an id in the body, title or branch, in that order.
func pickupTask(prefix string, hostIssues bool, p pull) string {
	if hostIssues && prefix != "" {
		repo := p.url()
		if i := pullPathRE.FindStringIndex(repo); i != nil {
			repo = strings.TrimSuffix(repo[:i[0]], "/-")
		}

		for _, m := range closingRE.FindAllStringSubmatch(p.body(), -1) {
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

	for _, text := range []string{p.body(), p.Title, p.head()} {
		if id := ref.Match(prefix, text); id != "" {
			return id
		}
	}

	return ""
}

func refusePull(p pull) error {
	switch {
	case p.fork():
		return fmt.Errorf("%s comes from a fork: PRs from forks are not supported yet", p.url())
	case p.status() == pullOpen && p.head() == "":
		return fmt.Errorf("%s has no head branch in the host's answer", p.url())
	case p.status() == pullMerged && p.sha() == "":
		return fmt.Errorf("%s is merged, but the host names no merge commit to check out", p.url())
	case p.status() == pullClosed && p.headSHA() == "":
		return fmt.Errorf("%s is closed, but the host names no head commit to check out", p.url())
	case p.status() != pullOpen && p.status() != pullMerged && p.status() != pullClosed:
		return fmt.Errorf("%s is %q: start picks up open, merged and closed PRs", p.url(), p.status())
	}

	return nil
}

// pickupOf reads the request through f10 pr, which reads one in any state on
// either host, so the prompt is the same text on GitHub and GitLab.
func (h host) pickupOf(number string, p pull) *pickup {
	pick := &pickup{
		number: number,
		url:    p.url(),
		branch: p.head(),
		view:   "f10 pr view --comments " + number,
		diff:   "f10 pr diff " + number,
	}

	switch p.status() {
	case pullMerged:
		pick.sha, pick.state = p.sha(), landedState(p)
	case pullClosed:
		pick.sha, pick.state = p.headSHA(), landedState(p)
		pick.headRef = h.Pick([]string{"refs/pull/" + number + "/head"}, []string{"refs/merge-requests/" + number + "/head"})[0]
	}

	return pick
}

// landedState is how a merged or closed request is named to the agent and
// in the report: who merged it and when, or that it closed unmerged.
func landedState(p pull) string {
	switch p.status() {
	case pullMerged:
		state := "merged"
		if by := p.mergedBy(); by != "" {
			state += " by " + by
		}

		return state + onDay(p.mergedAt())
	case pullClosed:
		return "closed without a merge" + onDay(p.closedAt())
	}

	return ""
}

func onDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return " on " + t.UTC().Format(time.DateOnly)
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
func fetchPickup(ctx context.Context, main string, pick *pickup) error {
	if err := gitx.Fetch(ctx, main); err != nil {
		return fmt.Errorf("fetching %s for %s: %w", pick.branch, pick.url, err)
	}

	if pick.headRef != "" {
		if err := gitx.FetchRef(ctx, main, pick.headRef); err != nil {
			return fmt.Errorf("fetching %s for %s: %w", pick.headRef, pick.url, err)
		}
	}

	if pick.landed() && refSHA(ctx, main, pick.sha+"^{commit}") == "" {
		return fmt.Errorf("%s is %s at %s, which origin does not hold after a fetch", pick.url, pick.state, pick.sha)
	}

	return nil
}
