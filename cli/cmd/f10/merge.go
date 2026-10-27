package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/forge"
)

// The host's side of `f10 finish`: read a branch's pull or merge request,
// merge it, delete its remote branch. Both CLIs are asked for JSON and read
// into one shape, as host.go does for issues, so finish decides on
// open / merged and never on a CLI's spelling of them.

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
	Title               string `json:"title"`
	Body                string `json:"body"`
	Description         string `json:"description"`
	IsCrossRepository   bool   `json:"isCrossRepository"`
	SourceProjectID     int    `json:"source_project_id"`
	TargetProjectID     int    `json:"target_project_id"`

	Author struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	} `json:"author"`

	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

// The two states finish acts on; anything else is a refusal that quotes
// the state. gh spells them OPEN / MERGED / CLOSED, glab opened / merged /
// closed / locked.
const (
	pullOpen   = forge.Open
	pullMerged = forge.Merged
)

func (p pull) url() string  { return cmp.Or(p.URL, p.WebURL) }
func (p pull) base() string { return cmp.Or(p.BaseRefName, p.TargetBranch) }
func (p pull) sha() string  { return cmp.Or(p.MergeCommit.OID, p.SquashCommitSHA, p.MergeCommitSHA) }
func (p pull) head() string { return cmp.Or(p.HeadRefName, p.SourceBranch) }
func (p pull) body() string { return cmp.Or(p.Body, p.Description) }

// id is the request's number as the host CLI takes it: gh's number, glab's iid.
func (p pull) id() string { return strconv.Itoa(cmp.Or(p.Number, p.IID)) }

// author is who opened the request: gh names it login, glab username.
func (p pull) author() string { return cmp.Or(p.Author.Login, p.Author.Username) }

// fork reports whether the request's branch lives in another repository,
// which origin does not have.
func (p pull) fork() bool {
	return p.IsCrossRepository || p.SourceProjectID != p.TargetProjectID
}

func (p pull) status() string { return forge.Status(p.State) }

// noSuchPull reports whether the host answered that there is no such
// request, by number or for a branch, as opposed to failing to answer at
// all. Every phrase either CLI uses for it lives here.
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

// pull reads the request whose head is branch. No request is the host's
// error, verbatim: finish has nothing to merge and says why in gh's or
// glab's words.
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

// pullBody reads the description of the request whose head is branch.
func (h host) pullBody(ctx context.Context, branch string) (string, error) {
	var p pull

	err := h.Decode(ctx, &p, h.Pick(
		[]string{"pr", "view", branch, "--json", "body"},
		[]string{"mr", "view", branch, "-F", "json"},
	)...)

	return p.body(), err
}

// editPullBody replaces the description of the request whose head is branch.
func (h host) editPullBody(ctx context.Context, branch, body string) error {
	if h.GLab() {
		_, err := h.Run(ctx, "mr", "update", branch, "--description", body)

		return err
	}

	f, err := os.CreateTemp("", "f10-pr-body-*.md")
	if err != nil {
		return fmt.Errorf("writing the PR body: %w", err)
	}

	defer os.Remove(f.Name())

	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()

		return fmt.Errorf("writing the PR body: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("writing the PR body: %w", err)
	}

	_, err = h.Run(ctx, "pr", "edit", branch, "--body-file", f.Name())

	return err
}

// pullNumber reads the request numbered number, with what start needs to
// pick it up: its head branch, where that lives, and the text a task id
// is read from.
func (h host) pullNumber(ctx context.Context, number string) (pull, error) {
	var p pull

	err := h.Decode(ctx, &p, h.Pick(
		[]string{"pr", "view", number, "--json", "state,url,number,title,body,author,headRefName,isCrossRepository"},
		[]string{"mr", "view", number, "-F", "json"},
	)...)

	return p, err
}

// mergePull merges the request with method and reads it back, since the
// merge commit exists only afterwards. Never --admin: a host that refuses
// has a reason, and it is the error. glab's auto-merge is turned off so a
// running pipeline makes it refuse rather than queue and report success.
// A body replaces the commit's default body: gh keeps its subject, glab
// takes the whole squash message, so the request's title goes on top. A
// glab merge commit never gets one: its method is "" and the project fixes
// its message.
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

// stackedPulls reads the open requests whose base is branch, past both
// CLIs' default page of 30: one left out would be closed by the delete.
func (h host) stackedPulls(ctx context.Context, branch string) ([]pull, error) {
	var ps []pull

	gh := []string{"pr", "list", "--base", branch, "--state", "open", "--limit", "1000"}

	err := h.Decode(ctx, &ps, h.Pick(
		append(gh, "--json", "number,url,headRefName"),
		[]string{"mr", "list", "--target-branch", branch, "--per-page", "100", "-F", "json"},
	)...)

	return ps, err
}

// retargetPull moves the request p onto base. It goes by number, since a
// stacked request's head is another task's branch.
func (h host) retargetPull(ctx context.Context, p pull, base string) error {
	_, err := h.Run(ctx, h.Pick(
		[]string{"pr", "edit", p.id(), "--base", base},
		[]string{"mr", "update", p.id(), "--target-branch", base},
	)...)

	return err
}

// deleteRemoteBranch deletes branch on origin through the host's API, the
// same call `gh pr merge --delete-branch` makes. That flag is not used
// because it also deletes the local branch, which git refuses while a
// worktree has it checked out, and gh then exits non-zero after the merge
// landed. Unlike the host's own delete on merge, this one closes every
// request still based on branch, so callers retarget them first.
func (h host) deleteRemoteBranch(ctx context.Context, branch string) error {
	_, err := h.Run(ctx, h.Pick(
		[]string{"api", "-X", "DELETE", "repos/{owner}/{repo}/git/refs/heads/" + branch},
		[]string{"api", "-X", "DELETE", "projects/:fullpath/repository/branches/" + branch},
	)...)

	return err
}

// mergeMethod is the flag the host CLI takes for this repo's merge: the
// method project.md declares, else the one the repo allows. GitLab fixes
// the strategy per project and leaves only squash to the request, so the
// project's squash option decides and "" means a plain merge. A GitHub repo
// allowing several gets squash with a note, since a default that rewrites
// history is not taken in silence.
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
