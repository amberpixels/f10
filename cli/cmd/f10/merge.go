package main

import (
	"cmp"
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
)

// The host's side of `f10 finish`: read a branch's pull or merge request,
// merge it, delete its remote branch.

// pull is the union of the two CLIs' request shapes, tagged for both.
type pull struct {
	State               string `json:"state"`
	URL                 string `json:"url"`
	WebURL              string `json:"web_url"`
	Number              int    `json:"number"`
	IID                 int    `json:"iid"`
	BaseRefName         string `json:"baseRefName"`
	TargetBranch        string `json:"target_branch"`
	MergeStateStatus    string `json:"mergeStateStatus"`
	DetailedMergeStatus string `json:"detailed_merge_status"`
	MergeCommitSHA      string `json:"merge_commit_sha"`
	SquashCommitSHA     string `json:"squash_commit_sha"`
	HeadRefName         string `json:"headRefName"`
	SourceBranch        string `json:"source_branch"`
	HeadRefOID          string `json:"headRefOid"`
	SHA                 string `json:"sha"`
	Title               string `json:"title"`
	Body                string `json:"body"`
	Description         string `json:"description"`
	IsCrossRepository   bool   `json:"isCrossRepository"`
	SourceProjectID     int    `json:"source_project_id"`
	TargetProjectID     int    `json:"target_project_id"`

	MergedAt   time.Time `json:"mergedAt"`
	MergedAtGL time.Time `json:"merged_at"`
	ClosedAt   time.Time `json:"closedAt"`
	ClosedAtGL time.Time `json:"closed_at"`

	Author   pullUser `json:"author"`
	MergedBy pullUser `json:"mergedBy"`
	MergedGL pullUser `json:"merged_by"`

	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

// A pullUser is a person as either CLI names one: gh by login, glab by username.
type pullUser struct {
	Login    string `json:"login"`
	Username string `json:"username"`
}

func (u pullUser) name() string { return cmp.Or(u.Login, u.Username) }

// The states finish and a pickup act on; any other is a refusal quoting the state.
const (
	pullOpen   = forge.Open
	pullMerged = forge.Merged
	pullClosed = forge.Closed
)

func (p pull) url() string     { return cmp.Or(p.URL, p.WebURL) }
func (p pull) base() string    { return cmp.Or(p.BaseRefName, p.TargetBranch) }
func (p pull) sha() string     { return cmp.Or(p.MergeCommit.OID, p.SquashCommitSHA, p.MergeCommitSHA) }
func (p pull) head() string    { return cmp.Or(p.HeadRefName, p.SourceBranch) }
func (p pull) headSHA() string { return cmp.Or(p.HeadRefOID, p.SHA) }
func (p pull) body() string    { return cmp.Or(p.Body, p.Description) }

func (p pull) mergedAt() time.Time { return cmp.Or(p.MergedAt, p.MergedAtGL) }
func (p pull) closedAt() time.Time { return cmp.Or(p.ClosedAt, p.ClosedAtGL) }
func (p pull) mergedBy() string    { return cmp.Or(p.MergedBy.name(), p.MergedGL.name()) }

// id is the request's number as the host CLI takes it: gh's number, glab's iid.
func (p pull) id() string { return strconv.Itoa(cmp.Or(p.Number, p.IID)) }

// author is who opened the request.
func (p pull) author() string { return p.Author.name() }

// fork reports whether the request's branch lives in another repository,
// which origin does not have.
func (p pull) fork() bool {
	return p.IsCrossRepository || p.SourceProjectID != p.TargetProjectID
}

func (p pull) status() string { return forge.Status(p.State) }

// noSuchPull tells "no such request" from a host that failed to answer at
// all; every phrase either CLI uses for it lives here.
func noSuchPull(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()

	for _, phrase := range []string{
		"Could not resolve to a PullRequest", // gh, by number
		"no pull requests found",             // gh, by branch
		"no open merge request",              // glab, by branch
		"404 Not Found",                      // glab, by number
	} {
		if strings.Contains(msg, phrase) {
			return true
		}
	}

	return false
}

// pull passes a missing request through as the host's own error, so finish
// says why in gh's or glab's words.
func (h host) pull(ctx context.Context, branch string) (pull, error) {
	var p pull

	err := h.Decode(ctx, &p, h.Pick(
		[]string{
			"pr",
			"view",
			branch,
			"--json",
			"state,url,number,title,body,baseRefName,mergeStateStatus,mergeCommit,author",
		},
		[]string{"mr", "view", branch, "-F", "json"},
	)...)

	return p, err
}

func (h host) pullBody(ctx context.Context, branch string) (string, error) {
	var p pull

	err := h.Decode(ctx, &p, h.Pick(
		[]string{"pr", "view", branch, "--json", "body"},
		[]string{"mr", "view", branch, "-F", "json"},
	)...)

	return p.body(), err
}

func (h host) editPullBody(ctx context.Context, branch, body string) error {
	return h.EditPull(ctx, branch, "", body)
}

// pickupFields is what a pickup reads of a request by number: the landed
// commit and who merged it serve a merged or closed one.
const pickupFields = "state,url,number,title,body,author,headRefName,headRefOid,isCrossRepository," +
	"mergeCommit,mergedAt,mergedBy,closedAt"

func (h host) pullNumber(ctx context.Context, number string) (pull, error) {
	var p pull

	err := h.Decode(ctx, &p, h.Pick(
		[]string{"pr", "view", number, "--json", pickupFields},
		[]string{"mr", "view", number, "-F", "json"},
	)...)

	return p, err
}

// mergePull reads the request back, since the merge commit exists only after.
// Never --admin: a refusing host has a reason. glab's auto-merge is off so a
// running pipeline refuses rather than queues; glab's squash message replaces
// the whole commit message, so the title goes on top, and a merge commit
// (method "") takes none, since the project fixes its message.
func (h host) mergePull(ctx context.Context, p pull, branch, method, body string) (pull, error) {
	args := h.Pick(
		[]string{"pr", "merge", branch},
		[]string{"mr", "merge", branch, "--remove-source-branch", "--auto-merge=false", "--yes"},
	)
	if method != "" {
		args = append(args, method)
	}

	if body != "" {
		flag := "--body"
		if h.GLab() {
			flag, body = "--squash-message", p.Title+"\n\n"+body
		}

		args = append(args, flag, body)
	}

	if _, err := h.Run(ctx, args...); err != nil {
		return pull{}, err
	}

	return h.pull(ctx, branch)
}

// stackedPulls pages past both CLIs' default 30: a request left out would be
// closed by the delete.
func (h host) stackedPulls(ctx context.Context, branch string) ([]pull, error) {
	var ps []pull

	gh := []string{"pr", "list", "--base", branch, "--state", "open", "--limit", "1000"}

	err := h.Decode(ctx, &ps, h.Pick(
		append(gh, "--json", "number,url,headRefName"),
		[]string{"mr", "list", "--target-branch", branch, "--per-page", "100", "-F", "json"},
	)...)

	return ps, err
}

// retargetPull goes by number: a stacked request's head is another task's
// branch.
func (h host) retargetPull(ctx context.Context, p pull, base string) error {
	_, err := h.Run(ctx, h.Pick(
		[]string{"pr", "edit", p.id(), "--base", base},
		[]string{"mr", "update", p.id(), "--target-branch", base},
	)...)

	return err
}

// deleteRemoteBranch goes through the API rather than `--delete-branch`, which
// also deletes the local branch, refused while a worktree holds it, so gh
// exits non-zero after the merge landed. Unlike the host's delete on merge
// this closes every request based on branch, so callers retarget them first.
func (h host) deleteRemoteBranch(ctx context.Context, branch string) error {
	segments := strings.Split(branch, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}

	_, err := h.Run(ctx, h.Pick(
		[]string{"api", "-X", "DELETE", "repos/{owner}/{repo}/git/refs/heads/" + strings.Join(segments, "/")},
		[]string{"api", "-X", "DELETE", "projects/:fullpath/repository/branches/" + url.PathEscape(branch)},
	)...)

	return err
}

// mergeMethod reads the project's squash option on GitLab, which fixes the
// strategy per project ("" is a plain merge); a GitHub repo allowing several
// gets squash with a note, since a history-rewriting default is not silent.
func (h host) mergeMethod(ctx context.Context, declared string) (string, string, error) {
	if declared != "" {
		method := strings.ToLower(declared)
		if h.GLab() && method == "merge" {
			return "", "", nil
		}

		return "--" + method, "", nil
	}

	if h.GLab() {
		var repo struct {
			SquashOption string `json:"squash_option"`
		}

		if err := h.Decode(ctx, &repo, "repo", "view", "-F", "json"); err != nil {
			return "", "", err
		}

		if repo.SquashOption == "always" || repo.SquashOption == "default_on" {
			return "--squash", "", nil
		}

		return "", "", nil
	}

	var repo struct {
		Squash bool `json:"squashMergeAllowed"`
		Merge  bool `json:"mergeCommitAllowed"`
		Rebase bool `json:"rebaseMergeAllowed"`
	}

	err := h.Decode(ctx, &repo, "repo", "view", "--json", "squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed")
	if err != nil {
		return "", "", err
	}

	var allowed []string

	for _, a := range []struct {
		ok   bool
		flag string
	}{{repo.Squash, "--squash"}, {repo.Merge, "--merge"}, {repo.Rebase, "--rebase"}} {
		if a.ok {
			allowed = append(allowed, a.flag)
		}
	}

	if len(allowed) == 1 {
		return allowed[0], "", nil
	}

	return "--squash", "merged with --squash: project.md declares no merge method and the repo allows several; " +
		"declare `merge method: squash` (or merge, rebase) under Hosting & PR to settle it", nil
}
