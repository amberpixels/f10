package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// Picking up a PR someone else opened: the task is in the tracker, the
// branch and the request are on the remote, and nothing is local. start
// takes the request's link or number, checks its branch out tracking
// origin's, and links the branch to its task in local config, since the
// branch's name need not carry the id.

// exitNoTask is start's exit when a picked-up request names no task: the
// start skill asks for the id and reruns with --task.
const exitNoTask = 3

// A pickup is the request start picked up: its number as the host CLI
// takes it, its url, its head branch, and the commands that show it.
type pickup struct {
	number string
	url    string
	branch string
	view   string // the request with its comments, in the host CLI's spelling
	diff   string
}

// pullPathRE is where a request's number sits in its url: GitHub's
// `/pull/12`, GitLab's `/-/merge_requests/12`.
var pullPathRE = regexp.MustCompile(`/(?:pull|merge_requests)/(\d+)(?:[/?#]|$)`)

// pullToken is the request number a start argument names, if any: a
// request url or GitLab's `!12` name one for sure; a bare `#12` may name a
// request or a task, so probe says to ask the host before taking it (only
// GitHub, whose issues and PRs share one counter, can answer).
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

// closingRE is a closing keyword and the issue it names, as both hosts read
// them in a request's body: `Closes #12`, `fixes: https://…/issues/12`.
var closingRE = regexp.MustCompile(
	`(?i)(?:^|[^a-z0-9])(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?\s+(#\d+|https?://\S+?/issues/\d+)`)

// pickupTask is the task id a request names, or "": a closing keyword's
// issue where the tracker is the host's issues, else an id in the project's
// format in the body, the title or the branch name, in that order. An issue
// url counts only when it is this repository's.
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

// refusePull stops a request start cannot pick up: one from a fork, whose
// branch origin does not have, and one no longer open.
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

// pickupOf is what start keeps of a request it picks up, with the commands
// the agent reads it by.
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

// syncPickup brings a picked-up PR's local branch, which start found
// already there, to the PR's head on origin: moved when it is behind and
// checked out nowhere, else a note saying how it differs.
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

// fetchPickup brings the request's branch into origin's refs, which a
// handover has not fetched yet.
func fetchPickup(ctx context.Context, main string, pick *pickup) error {
	if err := gitx.Fetch(ctx, main); err != nil {
		return fmt.Errorf("fetching %s for %s: %w", pick.branch, pick.url, err)
	}

	return nil
}
