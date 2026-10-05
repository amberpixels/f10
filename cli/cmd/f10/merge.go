package main

import (
	"cmp"
	"context"
	"regexp"
	"strings"
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

	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

// The two states finish acts on; anything else is a refusal that quotes
// the state. gh spells them OPEN / MERGED / CLOSED, glab opened / merged /
// closed / locked.
const (
	pullOpen   = "open"
	pullMerged = "merged"
)

func (p pull) url() string  { return cmp.Or(p.URL, p.WebURL) }
func (p pull) base() string { return cmp.Or(p.BaseRefName, p.TargetBranch) }
func (p pull) sha() string  { return cmp.Or(p.MergeCommit.OID, p.SquashCommitSHA, p.MergeCommitSHA) }

func (p pull) status() string {
	switch s := strings.ToLower(p.State); s {
	case "opened", "locked":
		return pullOpen
	default:
		return s
	}
}

// pull reads the request whose head is branch. No request is the host's
// error, verbatim: finish has nothing to merge and says why in gh's or
// glab's words.
func (h host) pull(ctx context.Context, branch string) (pull, error) {
	var p pull

	err := h.decode(ctx, &p, h.pick(
		[]string{"pr", "view", branch, "--json", "state,url,number,baseRefName,mergeStateStatus,mergeCommit"},
		[]string{"mr", "view", branch, "-F", "json"},
	)...)

	return p, err
}

// mergePull merges the request with method and reads it back, since the
// merge commit exists only afterwards. Never --admin: a host that refuses
// has a reason, and it is the error. glab's auto-merge is turned off so a
// running pipeline makes it refuse rather than queue and report success.
func (h host) mergePull(ctx context.Context, branch, method string) (pull, error) {
	args := h.pick(
		[]string{"pr", "merge", branch},
		[]string{"mr", "merge", branch, "--remove-source-branch", "--auto-merge=false", "--yes"},
	)
	if method != "" {
		args = append(args, method)
	}

	if _, err := h.run(ctx, args...); err != nil {
		return pull{}, err
	}

	return h.pull(ctx, branch)
}

// deleteRemoteBranch deletes branch on origin through the host's API, the
// same call `gh pr merge --delete-branch` makes. That flag is not used
// because it also deletes the local branch, which git refuses while a
// worktree has it checked out, and gh then exits non-zero after the merge
// landed.
func (h host) deleteRemoteBranch(ctx context.Context, branch string) error {
	_, err := h.run(ctx, h.pick(
		[]string{"api", "-X", "DELETE", "repos/{owner}/{repo}/git/refs/heads/" + branch},
		[]string{"api", "-X", "DELETE", "projects/:fullpath/repository/branches/" + branch},
	)...)

	return err
}

// mergeMethodRe is the phrase project.md may carry under Hosting & PR:
// `merge method: squash`, `merge: rebase`, in any case.
var mergeMethodRe = regexp.MustCompile(`(?i)\bmerge(?:\s+method)?\s*:\s*(squash|merge|rebase)\b`)

// mergeMethod is the flag the host CLI takes for this repo's merge: the
// method project.md declares, else the one the repo allows. On GitLab the
// server fixes the strategy per project and squash is the request's only
// call, so the absent case reads the project's squash option and "" means
// a plain merge. On GitHub a repo allowing several methods gets squash,
// and the note says so: a default that rewrites history is not taken in
// silence.
func (h host) mergeMethod(ctx context.Context, hosting string) (string, string, error) {
	if m := mergeMethodRe.FindStringSubmatch(hosting); m != nil {
		method := strings.ToLower(m[1])
		if h.glab() && method == "merge" {
			return "", "", nil
		}

		return "--" + method, "", nil
	}

	if h.glab() {
		var repo struct {
			SquashOption string `json:"squash_option"`
		}

		if err := h.decode(ctx, &repo, "repo", "view", "-F", "json"); err != nil {
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

	err := h.decode(ctx, &repo, "repo", "view", "--json", "squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed")
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
