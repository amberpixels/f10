package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// Picking up someone else's PR: nothing is local, so start checks the branch
// out tracking origin's and links it to its task in local config, since the
// branch's name need not carry the id.

// exitNoTask: the start skill asks for the id and reruns with --task.
const exitNoTask = 3

type pickup struct {
	number string
	url    string
	branch string
	view   string // the request with its comments, in the host CLI's spelling
	diff   string
}

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
	case p.status() != pullOpen:
		return fmt.Errorf("%s is %s: start picks up open PRs only", p.url(), p.status())
	case p.head() == "":
		return fmt.Errorf("%s has no head branch in the host's answer", p.url())
	}

	return nil
}

func (h host) pickupOf(number string, p pull) *pickup {
	cmd := func(verb string, tail ...string) string {
		args := h.Pick([]string{"pr", verb, number}, []string{"mr", verb, number})

		return strings.Join(append(append([]string{h.Name}, args...), tail...), " ")
	}

	return &pickup{
		number: number,
		url:    p.url(),
		branch: p.head(),
		view:   cmd("view", "--comments"),
		diff:   cmd("diff"),
	}
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

// fetchPickup: a handover has not fetched the request's branch yet.
func fetchPickup(ctx context.Context, main string, pick *pickup) error {
	if err := gitx.Fetch(ctx, main); err != nil {
		return fmt.Errorf("fetching %s for %s: %w", pick.branch, pick.url, err)
	}

	return nil
}
