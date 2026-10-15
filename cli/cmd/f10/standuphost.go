package main

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// The host's side of `f10 standup`: the PRs or MRs the user authored, and
// other people's they reviewed or merged, since a time. Each CLI is asked
// for JSON and read into one union shape, as merge.go does for finish.
// Heavy fields are asked for only where used: commit lists for the user's
// own requests, reviews and notes for the ones they reviewed.

// ghPRLimit caps each gh search. Reaching it is an error rather than a
// report silently missing the rest.
const ghPRLimit = 1000

const (
	ghOwnFields     = "number,title,state,isDraft,headRefName,baseRefName,url,author,createdAt,mergedAt,changedFiles,mergeCommit"
	ghForeignFields = "number,title,state,url,author,mergedAt,mergedBy"
)

// standupPull is the union of the two CLIs' request shapes, tagged for both.
type standupPull struct {
	Number       int       `json:"number"`
	IID          int       `json:"iid"`
	Title        string    `json:"title"`
	State        string    `json:"state"`
	URL          string    `json:"url"`
	WebURL       string    `json:"web_url"`
	IsDraft      bool      `json:"isDraft"`
	Draft        bool      `json:"draft"`
	HeadRefName  string    `json:"headRefName"`
	SourceBranch string    `json:"source_branch"`
	BaseRefName  string    `json:"baseRefName"`
	TargetBranch string    `json:"target_branch"`
	CreatedAt    time.Time `json:"createdAt"`
	CreatedAtGL  time.Time `json:"created_at"`
	MergedAt     time.Time `json:"mergedAt"`
	MergedAtGL   time.Time `json:"merged_at"`
	ChangedFiles int       `json:"changedFiles"`
	MergeSHA     string    `json:"merge_commit_sha"`
	SquashSHA    string    `json:"squash_commit_sha"`

	Author   standupUser `json:"author"`
	MergedBy standupUser `json:"mergedBy"`
	MergedGL standupUser `json:"merged_by"`

	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`

	Reviews []struct {
		Author      standupUser `json:"author"`
		SubmittedAt time.Time   `json:"submittedAt"`
	} `json:"reviews"`
}

type standupUser struct {
	Login    string `json:"login"`
	Username string `json:"username"`
}

func (u standupUser) name() string { return cmp.Or(u.Login, u.Username) }

func (p standupPull) number() int { return cmp.Or(p.Number, p.IID) }

func (p standupPull) hostPR() standup.HostPR {
	out := standup.HostPR{
		Number:    p.number(),
		Title:     p.Title,
		URL:       cmp.Or(p.URL, p.WebURL),
		Status:    pull{State: p.State}.status(),
		Draft:     p.IsDraft || p.Draft,
		Empty:     p.HeadRefName != "" && p.ChangedFiles == 0,
		Head:      cmp.Or(p.HeadRefName, p.SourceBranch),
		Base:      cmp.Or(p.BaseRefName, p.TargetBranch),
		Author:    p.Author.name(),
		CreatedAt: cmp.Or(p.CreatedAt, p.CreatedAtGL),
		MergedAt:  cmp.Or(p.MergedAt, p.MergedAtGL),
		MergedBy:  cmp.Or(p.MergedBy.name(), p.MergedGL.name()),
	}

	for _, sha := range []string{p.MergeCommit.OID, p.MergeSHA, p.SquashSHA} {
		if sha != "" {
			out.MergeSHA = append(out.MergeSHA, sha)
		}
	}

	for _, r := range p.Reviews {
		out.Reviews = append(out.Reviews, standup.Review{Author: r.Author.name(), At: r.SubmittedAt})
	}

	return out
}

// standupPulls is every request the report needs: the user's own, with
// their commits, and others' the user reviewed or merged since since.
func (h host) standupPulls(ctx context.Context, login string, since time.Time) ([]standup.HostPR, error) {
	if h.glab() {
		return h.standupMRs(ctx, login, since)
	}

	stamp := since.UTC().Format(time.RFC3339)

	own, err := h.searchPRs(ctx, "author:"+login+" updated:>="+stamp, ghOwnFields)
	if err != nil {
		return nil, err
	}

	reviewed, err := h.searchPRs(ctx, "reviewed-by:"+login+" updated:>="+stamp, ghForeignFields+",reviews")
	if err != nil {
		return nil, err
	}

	merged, err := h.searchPRs(ctx, "is:merged merged:>="+stamp, ghForeignFields)
	if err != nil {
		return nil, err
	}

	out := make([]standup.HostPR, 0, len(own))

	for _, p := range own {
		hp := p.hostPR()

		if hp.Commits, err = h.pullCommits(ctx, hp.Number); err != nil {
			return nil, err
		}

		if hp.ForcePushes, err = h.prForcePushes(ctx, hp.Number, login); err != nil {
			return nil, err
		}

		out = append(out, hp)
	}

	return append(out, foreignPulls(login, reviewed, merged)...), nil
}

// defaultBranchName is the repo's default branch as the host has it.
func (h host) defaultBranchName(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		DefaultBranch string `json:"default_branch"`
	}

	err := h.decode(ctx, &repo, h.pick(
		[]string{"repo", "view", "--json", "defaultBranchRef"},
		[]string{"repo", "view", "-F", "json"},
	)...)
	if err != nil {
		return "", err
	}

	name := cmp.Or(repo.DefaultBranchRef.Name, repo.DefaultBranch)
	if name == "" {
		return "", fmt.Errorf("%s returned no default branch", h.name)
	}

	return name, nil
}

// pullCommits is the commits a request lists, one call per request: asked
// for inside the list query, gh's GraphQL refuses a node count past its
// limit.
func (h host) pullCommits(ctx context.Context, number int) ([]standup.PRCommit, error) {
	path := h.pick(
		[]string{fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/commits?per_page=100", number)},
		[]string{fmt.Sprintf("projects/:fullpath/merge_requests/%d/commits?per_page=100", number)},
	)[0]

	commits, err := decodePages[struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`

		ID           string    `json:"id"`
		Title        string    `json:"title"`
		AuthoredDate time.Time `json:"authored_date"`
	}](ctx, h, path)
	if err != nil {
		return nil, err
	}

	out := make([]standup.PRCommit, 0, len(commits))

	for _, c := range commits {
		subject, _, _ := strings.Cut(cmp.Or(c.Commit.Message, c.Title), "\n")
		out = append(out, standup.PRCommit{
			SHA: cmp.Or(c.SHA, c.ID), Subject: subject, At: cmp.Or(c.Commit.Author.Date, c.AuthoredDate),
		})
	}

	return out, nil
}

func (h host) searchPRs(ctx context.Context, query, fields string) ([]standupPull, error) {
	var found []standupPull

	err := h.decode(ctx, &found, "pr", "list", "--state", "all", "--search", query,
		"--json", fields, "--limit", strconv.Itoa(ghPRLimit))
	if err != nil {
		return nil, err
	}

	if len(found) >= ghPRLimit {
		return nil, fmt.Errorf("gh found %d or more pull requests for %q: use a later --since", ghPRLimit, query)
	}

	return found, nil
}

// foreignPulls merges the reviewed and merged lists into one entry per
// request, the user's own dropped: their own query read those in full.
func foreignPulls(login string, reviewed, merged []standupPull) []standup.HostPR {
	var out []standup.HostPR

	index := map[int]int{}

	for _, p := range append(reviewed, merged...) {
		if sameLogin(login, p.Author.name()) {
			continue
		}

		hp := p.hostPR()
		if i, ok := index[hp.Number]; ok {
			out[i].Reviews = append(out[i].Reviews, hp.Reviews...)
			out[i].MergedBy = cmp.Or(out[i].MergedBy, hp.MergedBy)
			out[i].MergedAt = cmp.Or(out[i].MergedAt, hp.MergedAt)

			continue
		}

		index[hp.Number] = len(out)
		out = append(out, hp)
	}

	return out
}

// standupMRs is standupPulls on GitLab: the MR list API filters by author,
// reviewer and update time where glab mr list cannot. A reviewer's part is
// read from the MR's notes: an approval leaves a system note, a comment a
// plain one, and both carry the time the list's reviewers field lacks.
func (h host) standupMRs(ctx context.Context, login string, since time.Time) ([]standup.HostPR, error) {
	list := func(filter string) ([]standupPull, error) {
		q := url.Values{"state": {"all"}, "updated_after": {since.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
		k, v, _ := strings.Cut(filter, "=")
		q.Set(k, v)

		return decodePages[standupPull](ctx, h, "projects/:fullpath/merge_requests?"+q.Encode())
	}

	own, err := list("author_username=" + login)
	if err != nil {
		return nil, err
	}

	asReviewer, err := list("reviewer_username=" + login)
	if err != nil {
		return nil, err
	}

	merged, err := list("state=merged")
	if err != nil {
		return nil, err
	}

	out := make([]standup.HostPR, 0, len(own))

	for _, p := range own {
		hp := p.hostPR()

		if hp.Commits, err = h.pullCommits(ctx, hp.Number); err != nil {
			return nil, err
		}

		hp.Empty = len(hp.Commits) == 0

		if hp.ForcePushes, err = h.mrForcePushes(ctx, hp.Number, since); err != nil {
			return nil, err
		}

		out = append(out, hp)
	}

	foreign := foreignPulls(login, asReviewer, merged)

	for i, p := range foreign {
		if !slices.ContainsFunc(asReviewer, func(r standupPull) bool { return r.number() == p.Number }) {
			continue
		}

		reviews, err := h.mrReviews(ctx, p.Number)
		if err != nil {
			return nil, err
		}

		foreign[i].Reviews = reviews
	}

	return append(out, foreign...), nil
}

// mrReviews is the notes that count as reviewing: any plain comment, and
// the system note an approval leaves.
func (h host) mrReviews(ctx context.Context, iid int) ([]standup.Review, error) {
	type note struct {
		Body      string      `json:"body"`
		System    bool        `json:"system"`
		CreatedAt time.Time   `json:"created_at"`
		Author    standupUser `json:"author"`
	}

	notes, err := decodePages[note](ctx, h, fmt.Sprintf("projects/:fullpath/merge_requests/%d/notes?per_page=100", iid))
	if err != nil {
		return nil, err
	}

	var out []standup.Review

	for _, n := range notes {
		if n.System && !strings.HasPrefix(n.Body, "approved this merge request") {
			continue
		}

		out = append(out, standup.Review{Author: n.Author.name(), At: n.CreatedAt})
	}

	return out, nil
}

const forcePushesQuery = "query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) " +
	"{ pullRequest(number: $number) { timelineItems(last: 100, itemTypes: [HEAD_REF_FORCE_PUSHED_EVENT]) " +
	"{ nodes { ... on HeadRefForcePushedEvent { createdAt actor { login } beforeCommit { oid } afterCommit { oid } } } } } } }"

// prForcePushes is the force-pushes the user made to a PR, the last 100:
// GitHub records each with the head before and after.
func (h host) prForcePushes(ctx context.Context, number int, login string) ([]standup.ForcePush, error) {
	var payload struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					TimelineItems struct {
						Nodes []struct {
							CreatedAt    time.Time   `json:"createdAt"`
							Actor        standupUser `json:"actor"`
							BeforeCommit struct {
								OID string `json:"oid"`
							} `json:"beforeCommit"`
							AfterCommit struct {
								OID string `json:"oid"`
							} `json:"afterCommit"`
						} `json:"nodes"`
					} `json:"timelineItems"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}

	err := h.decode(ctx, &payload, "api", "graphql", "-f", "query="+forcePushesQuery,
		"-F", "owner={owner}", "-F", "name={repo}", "-F", "number="+strconv.Itoa(number))
	if err != nil {
		return nil, err
	}

	var out []standup.ForcePush

	for _, n := range payload.Data.Repository.PullRequest.TimelineItems.Nodes {
		if !sameLogin(login, n.Actor.name()) {
			continue
		}

		out = append(out, standup.ForcePush{At: n.CreatedAt, Before: n.BeforeCommit.OID, After: n.AfterCommit.OID})
	}

	return out, nil
}

// mrForcePushes reads GitLab's MR versions, one per push, since the time:
// a version whose previous head is not its ancestor replaced commits. GitLab
// records no pusher, so on the user's own MR each counts as theirs.
func (h host) mrForcePushes(ctx context.Context, iid int, since time.Time) ([]standup.ForcePush, error) {
	type version struct {
		Head      string    `json:"head_commit_sha"`
		CreatedAt time.Time `json:"created_at"`
	}

	versions, err := decodePages[version](
		ctx,
		h,
		fmt.Sprintf("projects/:fullpath/merge_requests/%d/versions?per_page=100", iid),
	)
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(versions, func(a, b version) int { return a.CreatedAt.Compare(b.CreatedAt) })

	var out []standup.ForcePush

	for i := 1; i < len(versions); i++ {
		prev, cur := versions[i-1], versions[i]
		if cur.CreatedAt.Before(since) || gitx.IsAncestor(ctx, h.dir, prev.Head, cur.Head) {
			continue
		}

		out = append(out, standup.ForcePush{At: cur.CreatedAt, Before: prev.Head, After: cur.Head})
	}

	return out, nil
}

// commitCI is how the checks on a commit ended, nil when it has none. On
// GitHub that is its check runs and its commit statuses; on GitLab its
// last pipeline.
func (h host) commitCI(ctx context.Context, sha string) (*standup.CI, error) {
	if h.glab() {
		var commit struct {
			LastPipeline *struct {
				Status    string    `json:"status"`
				UpdatedAt time.Time `json:"updated_at"`
			} `json:"last_pipeline"`
		}

		if err := h.decode(ctx, &commit, "api", "projects/:fullpath/repository/commits/"+sha); err != nil {
			return nil, err
		}

		if p := commit.LastPipeline; p != nil {
			return &standup.CI{Red: p.Status == "failed", At: p.UpdatedAt}, nil
		}

		return nil, nil
	}

	var runs struct {
		CheckRuns []struct {
			Conclusion  string    `json:"conclusion"`
			CompletedAt time.Time `json:"completed_at"`
		} `json:"check_runs"`
	}

	if err := h.decode(ctx, &runs, "api", "repos/{owner}/{repo}/commits/"+sha+"/check-runs?per_page=100"); err != nil {
		return nil, err
	}

	var status struct {
		Statuses []struct {
			State     string    `json:"state"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"statuses"`
	}

	if err := h.decode(ctx, &status, "api", "repos/{owner}/{repo}/commits/"+sha+"/status"); err != nil {
		return nil, err
	}

	var ci *standup.CI

	seen := func(red bool, at time.Time) {
		if ci == nil {
			ci = &standup.CI{}
		}

		if red && !ci.At.After(at) {
			ci.Red, ci.At = true, at
		}
	}

	for _, r := range runs.CheckRuns {
		if !r.CompletedAt.IsZero() {
			seen(r.Conclusion == "failure" || r.Conclusion == "timed_out", r.CompletedAt)
		}
	}

	for _, s := range status.Statuses {
		if s.State != "pending" {
			seen(s.State == "failure" || s.State == "error", s.UpdatedAt)
		}
	}

	return ci, nil
}
