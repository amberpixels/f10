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
