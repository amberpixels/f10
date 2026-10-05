package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// The host's side of `f10 finish`: read a branch's pull or merge request,
// merge it, delete its remote branch. Both CLIs are asked for JSON and read
// into one shape, as host.go does for issues, so finish decides on
// open / merged / closed and never on a CLI's spelling of them.

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

// The three states finish acts on. gh spells them OPEN / MERGED / CLOSED,
// glab opened / merged / closed / locked.
const (
	pullOpen   = "open"
	pullMerged = "merged"
	pullClosed = "closed"
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

// viewPull reads the request whose head is branch. No request is the host's
// error, verbatim: finish has nothing to merge and says why in gh's or
// glab's words.
func viewPull(ctx context.Context, dir, host, branch string) (pull, error) {
	args := []string{"pr", "view", branch, "--json", "state,url,number,baseRefName,mergeStateStatus,mergeCommit"}
	if host == "glab" {
		args = []string{"mr", "view", branch, "-F", "json"}
	}

	out, err := hostRun(ctx, dir, host, args...)
	if err != nil {
		return pull{}, err
	}

	var p pull
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return pull{}, fmt.Errorf("parsing %s output: %w", host, err)
	}

	return p, nil
}

// mergePull merges the request with method and reads it back, since the
// merge commit exists only afterwards. Never --admin: a host that refuses
// has a reason, and it is the error. glab's auto-merge is turned off so a
// running pipeline makes it refuse rather than queue and report success.
func mergePull(ctx context.Context, dir, host, branch, method string) (pull, error) {
	args := []string{"pr", "merge", branch, method}
	if host == "glab" {
		args = []string{"mr", "merge", branch, "--remove-source-branch", "--auto-merge=false", "--yes"}
		if method != "" {
			args = append(args, method)
		}
	}

	if _, err := hostRun(ctx, dir, host, args...); err != nil {
		return pull{}, err
	}

	return viewPull(ctx, dir, host, branch)
}

// deleteRemoteBranch deletes branch on origin through the host's API, the
// same call `gh pr merge --delete-branch` makes. That flag is not used
// because it also deletes the local branch, which git refuses while a
// worktree has it checked out, and gh then exits non-zero after the merge
// landed.
func deleteRemoteBranch(ctx context.Context, dir, host, branch string) error {
	args := []string{"api", "-X", "DELETE", "repos/{owner}/{repo}/git/refs/heads/" + branch}
	if host == "glab" {
		args = []string{"api", "-X", "DELETE", "projects/:fullpath/repository/branches/" + branch}
	}

	_, err := hostRun(ctx, dir, host, args...)

	return err
}

// mergeMethodRe is the phrase project.md may carry under Hosting & PR:
// `merge method: squash`, `merge: rebase`, in any case.
var mergeMethodRe = regexp.MustCompile(`(?i)\bmerge(?:\s+method)?\s*:\s*(squash|merge|rebase)\b`)

// mergeMethod is the flag the host CLI takes for this repo's merge: the
// method project.md declares, else the one the repo allows, else squash.
// On GitLab the server fixes the strategy per project and squash is the
// request's only call, so the absent case reads the project's squash
// option and "" means a plain merge.
func mergeMethod(ctx context.Context, dir, host, hosting string) (string, error) {
	if m := mergeMethodRe.FindStringSubmatch(hosting); m != nil {
		method := strings.ToLower(m[1])
		if host == "glab" && method == "merge" {
			return "", nil
		}

		return "--" + method, nil
	}

	if host == "glab" {
		out, err := hostRun(ctx, dir, host, "repo", "view", "-F", "json")
		if err != nil {
			return "", err
		}

		var repo struct {
			SquashOption string `json:"squash_option"`
		}

		if err := json.Unmarshal([]byte(out), &repo); err != nil {
			return "", fmt.Errorf("parsing glab repo: %w", err)
		}

		if repo.SquashOption == "always" || repo.SquashOption == "default_on" {
			return "--squash", nil
		}

		return "", nil
	}

	out, err := hostRun(ctx, dir, host, "repo", "view", "--json",
		"squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed")
	if err != nil {
		return "", err
	}

	var repo struct {
		Squash bool `json:"squashMergeAllowed"`
		Merge  bool `json:"mergeCommitAllowed"`
		Rebase bool `json:"rebaseMergeAllowed"`
	}

	if err := json.Unmarshal([]byte(out), &repo); err != nil {
		return "", fmt.Errorf("parsing gh repo: %w", err)
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
		return allowed[0], nil
	}

	return "--squash", nil
}

// hostRun runs one host CLI command in dir and returns its stdout. A
// non-zero exit is the CLI's stderr, verbatim.
func hostRun(ctx context.Context, dir, host string, args ...string) (string, error) {
	res, err := shell.Capture(ctx, dir, host, args...)
	if err != nil {
		return "", fmt.Errorf("running %s: %w", host, err)
	}

	if res.Code != 0 {
		return "", fmt.Errorf("%s: %s", host, cmp.Or(res.Stderr, res.Stdout, fmt.Sprintf("exit %d", res.Code)))
	}

	return res.Stdout, nil
}
