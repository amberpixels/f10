package forge

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// prFields is what every request read asks gh for: enough to view it, land
// it, pick it up, and tell a fork's branch from origin's.
const prFields = "number,title,state,url,body,author,baseRefName,headRefName,headRefOid," +
	"isCrossRepository,mergeStateStatus,mergeCommit,mergedAt,mergedBy,closedAt"

// PR reads one request by branch or number. A missing one is ErrNotFound,
// its text the CLI's own words.
func (h Host) PR(ctx context.Context, selector string) (PR, error) {
	var p pull

	err := h.decode(ctx, &p, h.pick(
		[]string{"pr", "view", selector, "--json", prFields},
		[]string{"mr", "view", selector, "-F", "json"},
	)...)

	return p.pr(), err
}

// OpenPRs is the first page of open requests, newest first.
func (h Host) OpenPRs(ctx context.Context, limit int) ([]PR, error) {
	var found []pull

	err := h.decode(ctx, &found, h.pick(
		[]string{"pr", "list", "--json", "number,title,state,url", "--limit", strconv.Itoa(limit)},
		[]string{"mr", "list", "-F", "json", "--per-page", strconv.Itoa(limit)},
	)...)

	out := make([]PR, 0, len(found))
	for _, p := range found {
		out = append(out, p.pr())
	}

	return out, err
}

// StackedPRs is every open request based on branch, paged past both CLIs'
// default 30: one left out would be closed by the branch's delete.
func (h Host) StackedPRs(ctx context.Context, branch string) ([]PR, error) {
	var found []pull

	err := h.decode(ctx, &found, h.pick(
		[]string{
			"pr",
			"list",
			"--base",
			branch,
			"--state",
			"open",
			"--limit",
			"1000",
			"--json",
			"number,url,headRefName",
		},
		[]string{"mr", "list", "--target-branch", branch, "--per-page", "100", "-F", "json"},
	)...)

	out := make([]PR, 0, len(found))
	for _, p := range found {
		out = append(out, p.pr())
	}

	return out, err
}

// ShowPR prints the request to the terminal through the CLI, or opens it in
// the browser.
func (h Host) ShowPR(ctx context.Context, number string, web bool) error {
	args := h.pick([]string{"pr", "view", number}, []string{"mr", "view", number})
	if web {
		args = append(args, "--web")
	}

	return shell.Passthrough(ctx, h.Dir, h.Name, h.named(args)...)
}

// Sigil is how the host writes a request's number: #n, or GitLab's !n.
func (h Host) Sigil() string {
	if h.glab() {
		return "!"
	}

	return "#"
}

// SharedNumbers reports whether issues and requests share one counter, so
// a bare #n may name either; GitLab numbers them apart.
func (h Host) SharedNumbers() bool { return !h.glab() }

// HeadRef is the ref a request's head lives under on origin, fetchable
// after its branch is deleted.
func (h Host) HeadRef(number string) string {
	if h.glab() {
		return "refs/merge-requests/" + number + "/head"
	}

	return "refs/pull/" + number + "/head"
}

// MergePR merges the request on branch and reads it back, since the merge
// commit exists only after. method is "--squash", "--merge", "--rebase", or
// "" for GitLab's plain merge, where the project fixes the strategy.
//
// Never --admin: a refusing host has a reason. glab's auto-merge is off so
// a running pipeline refuses rather than queues; its squash message
// replaces the whole commit message, so the title goes on top. gh's squash
// gets the title as its subject, else GitHub uses a lone commit's subject.
func (h Host) MergePR(ctx context.Context, p PR, branch, method, body string) (PR, error) {
	args := h.pick(
		[]string{"pr", "merge", branch},
		[]string{"mr", "merge", branch, "--remove-source-branch", "--auto-merge=false", "--yes"},
	)
	if method != "" {
		args = append(args, method)
	}

	if !h.glab() && method == "--squash" && p.Title != "" {
		args = append(args, "--subject", p.Title+" (#"+strconv.Itoa(p.Number)+")")
	}

	if body != "" {
		flag := "--body"
		if h.glab() {
			flag, body = "--squash-message", p.Title+"\n\n"+body
		}

		args = append(args, flag, body)
	}

	if _, err := h.run(ctx, args...); err != nil {
		return PR{}, err
	}

	return h.PR(ctx, branch)
}

// Retarget moves a request to base, by number: a stacked request's head is
// another task's branch.
func (h Host) Retarget(ctx context.Context, number int, base string) error {
	n := strconv.Itoa(number)

	_, err := h.run(ctx, h.pick(
		[]string{"pr", "edit", n, "--base", base},
		[]string{"mr", "update", n, "--target-branch", base},
	)...)

	return err
}

// DeleteBranch deletes branch on the host through the API rather than
// `--delete-branch`, which also deletes the local branch, refused while a
// worktree holds it, so gh exits non-zero after the merge landed. Unlike
// the host's delete on merge this closes every request based on branch, so
// callers retarget them first.
func (h Host) DeleteBranch(ctx context.Context, branch string) error {
	segments := strings.Split(branch, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}

	_, err := h.run(ctx, h.pick(
		[]string{"api", "-X", "DELETE", "repos/{owner}/{repo}/git/refs/heads/" + strings.Join(segments, "/")},
		[]string{"api", "-X", "DELETE", "projects/:fullpath/repository/branches/" + url.PathEscape(branch)},
	)...)

	return err
}

// MergeMethods is what the repository allows, as MergePR methods. GitLab
// fixes one strategy per project: "--squash" when its squash option is on,
// else "" for its plain merge.
func (h Host) MergeMethods(ctx context.Context) ([]string, error) {
	if h.glab() {
		var repo struct {
			SquashOption string `json:"squash_option"`
		}

		if err := h.decode(ctx, &repo, "repo", "view", "-F", "json"); err != nil {
			return nil, err
		}

		if repo.SquashOption == "always" || repo.SquashOption == "default_on" {
			return []string{"--squash"}, nil
		}

		return []string{""}, nil
	}

	var repo struct {
		Squash bool `json:"squashMergeAllowed"`
		Merge  bool `json:"mergeCommitAllowed"`
		Rebase bool `json:"rebaseMergeAllowed"`
	}

	err := h.decode(ctx, &repo, "repo", "view", "--json", "squashMergeAllowed,mergeCommitAllowed,rebaseMergeAllowed")
	if err != nil {
		return nil, err
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

	return allowed, nil
}

// MethodFor is a declared merge method (squash, merge, rebase) as MergePR
// takes it: GitLab's "merge" is its plain merge, which takes no flag.
func (h Host) MethodFor(declared string) string {
	method := strings.ToLower(declared)
	if h.glab() && method == "merge" {
		return ""
	}

	return "--" + method
}

// PRBody is the request's description alone, read fresh right before a
// write that must not act on a stale one.
func (h Host) PRBody(ctx context.Context, branch string) (string, error) {
	var p pull

	err := h.decode(ctx, &p, h.pick(
		[]string{"pr", "view", branch, "--json", "body"},
		[]string{"mr", "view", branch, "-F", "json"},
	)...)

	return p.pr().Body, err
}
