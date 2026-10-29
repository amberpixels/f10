package forge

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
)

// The pull requests a host lists, in one shape for both CLIs. Heavy fields
// (commits, reviews, notes) are asked for only where a reader uses them.

// PRLimit caps each gh search. Reaching it is an error rather than a
// report silently missing the rest.
const PRLimit = 1000

// The gh field sets: a person's own requests, other people's, and what a
// board wants to know about every request.
const (
	OwnFields     = "number,title,state,isDraft,headRefName,baseRefName,url,author,createdAt,mergedAt,changedFiles,mergeCommit"
	ForeignFields = "number,title,state,url,author,mergedAt,mergedBy"
	BoardFields   = "number,title,state,isDraft,headRefName,baseRefName,headRefOid,url,author,assignees," +
		"reviewRequests,reviews,reviewDecision,mergeable,mergeStateStatus,statusCheckRollup," +
		"createdAt,updatedAt,mergedAt,mergedBy,closedAt,body,mergeCommit"
)

// A PR is a pull or merge request as the host reported it.
type PR struct {
	Number    int
	Title     string
	URL       string
	Status    string // Open, Merged or Closed
	Draft     bool
	Empty     bool // no changes yet: no changed files on GitHub, no commits on GitLab
	Head      string
	HeadSHA   string
	Base      string
	Author    string
	Assignees []string
	Reviewers []string // reviewers requested and not yet heard from
	CreatedAt time.Time
	UpdatedAt time.Time
	MergedAt  time.Time // zero unless merged
	ClosedAt  time.Time // zero unless closed
	MergedBy  string
	Body      string
	Decision  string     // Approved, ChangesRequested, ReviewRequired or ""
	Mergeable string     // Clean, Conflicts, Blocked or ""
	Checks    string     // Green, Red, Pending or "" with none
	Commits   []PRCommit // the commits the host lists for it, where asked for
	MergeSHA  []string   // the merge and squash commits it landed as
	Reviews   []Review   // where asked for

	ForcePushes []ForcePush // the requester's own, where asked for
}

// A PRCommit is one commit a PR lists. A rebase merge lands it on the
// default branch under a new sha, keeping its author date and subject.
type PRCommit struct {
	SHA     string
	Subject string
	Message string // the whole message, trailers included
	Email   string // the author's email, as the commit records it
	At      time.Time
	Parents int // more than one is a merge
}

// A Review is one review event on a PR: a submitted review, an approval or
// a comment, by anyone.
type Review struct {
	Author string
	State  string // Approved, ChangesRequested, Commented or ""
	At     time.Time
}

// A ForcePush is one force-push to a PR: the head it replaced and the one
// it set.
type ForcePush struct {
	At     time.Time
	Before string
	After  string
	// SamePatches is whether every commit kept its patch, nil when git
	// cannot tell; the reader that owns the checkout fills it.
	SamePatches *bool
}

// CI is how the checks on a commit ended: Red when any failed, At the time
// of the last failure.
type CI struct {
	Red bool
	At  time.Time
}

// The vocabularies the host's words are read into.
const (
	Approved         = "approved"
	ChangesRequested = "changes_requested"
	ReviewRequired   = "review_required"
	Commented        = "commented"

	Clean     = "clean"
	Conflicts = "conflicts"
	Blocked   = "blocked"

	Green   = "green"
	Red     = "red"
	Pending = "pending"
)

// pull is the union of the two CLIs' request shapes, tagged for both.
type pull struct {
	Number       int       `json:"number"`
	IID          int       `json:"iid"`
	Title        string    `json:"title"`
	State        string    `json:"state"`
	URL          string    `json:"url"`
	WebURL       string    `json:"web_url"`
	IsDraft      bool      `json:"isDraft"`
	Draft        bool      `json:"draft"`
	HeadRefName  string    `json:"headRefName"`
	HeadRefOID   string    `json:"headRefOid"`
	SourceBranch string    `json:"source_branch"`
	SHA          string    `json:"sha"`
	BaseRefName  string    `json:"baseRefName"`
	TargetBranch string    `json:"target_branch"`
	CreatedAt    time.Time `json:"createdAt"`
	CreatedAtGL  time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updatedAt"`
	UpdatedAtGL  time.Time `json:"updated_at"`
	MergedAt     time.Time `json:"mergedAt"`
	MergedAtGL   time.Time `json:"merged_at"`
	ClosedAt     time.Time `json:"closedAt"`
	ClosedAtGL   time.Time `json:"closed_at"`
	ChangedFiles int       `json:"changedFiles"`
	MergeSHA     string    `json:"merge_commit_sha"`
	SquashSHA    string    `json:"squash_commit_sha"`
	Body         string    `json:"body"`
	Description  string    `json:"description"`

	Author   user `json:"author"`
	MergedBy user `json:"mergedBy"`
	MergedGL user `json:"merged_by"`

	Assignees   []user `json:"assignees"`
	ReviewersGL []user `json:"reviewers"`

	ReviewRequests []struct {
		Login string `json:"login"`
		Name  string `json:"name"` // a team
	} `json:"reviewRequests"`

	ReviewDecision      string `json:"reviewDecision"`
	Mergeable           string `json:"mergeable"`
	MergeStateStatus    string `json:"mergeStateStatus"`
	DetailedMergeStatus string `json:"detailed_merge_status"`

	StatusCheckRollup []struct {
		Status     string `json:"status"`     // a check run: COMPLETED, IN_PROGRESS, QUEUED
		Conclusion string `json:"conclusion"` // a check run: SUCCESS, FAILURE, ...
		State      string `json:"state"`      // a status context: SUCCESS, FAILURE, PENDING, ERROR
	} `json:"statusCheckRollup"`

	HeadPipeline *struct {
		Status string `json:"status"`
	} `json:"head_pipeline"`
	Pipeline *struct {
		Status string `json:"status"`
	} `json:"pipeline"`

	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`

	Reviews []struct {
		Author      user      `json:"author"`
		State       string    `json:"state"`
		SubmittedAt time.Time `json:"submittedAt"`
	} `json:"reviews"`
}

type user struct {
	Login    string `json:"login"`
	Username string `json:"username"`
}

func (u user) name() string { return cmp.Or(u.Login, u.Username) }

func (p pull) number() int { return cmp.Or(p.Number, p.IID) }

func (p pull) pr() PR {
	out := PR{
		Number:    p.number(),
		Title:     p.Title,
		URL:       cmp.Or(p.URL, p.WebURL),
		Status:    Status(p.State),
		Draft:     p.IsDraft || p.Draft,
		Empty:     p.HeadRefName != "" && p.ChangedFiles == 0,
		Head:      cmp.Or(p.HeadRefName, p.SourceBranch),
		HeadSHA:   cmp.Or(p.HeadRefOID, p.SHA),
		Base:      cmp.Or(p.BaseRefName, p.TargetBranch),
		Author:    p.Author.name(),
		CreatedAt: cmp.Or(p.CreatedAt, p.CreatedAtGL),
		UpdatedAt: cmp.Or(p.UpdatedAt, p.UpdatedAtGL),
		MergedAt:  cmp.Or(p.MergedAt, p.MergedAtGL),
		ClosedAt:  cmp.Or(p.ClosedAt, p.ClosedAtGL),
		MergedBy:  cmp.Or(p.MergedBy.name(), p.MergedGL.name()),
		Body:      cmp.Or(p.Body, p.Description),
		Decision:  decision(p.ReviewDecision),
		Mergeable: mergeable(p.Mergeable, p.MergeStateStatus, p.DetailedMergeStatus),
		Checks:    p.checks(),
	}

	for _, sha := range []string{p.MergeCommit.OID, p.MergeSHA, p.SquashSHA} {
		if sha != "" {
			out.MergeSHA = append(out.MergeSHA, sha)
		}
	}

	for _, u := range p.Assignees {
		out.Assignees = append(out.Assignees, u.name())
	}

	for _, r := range p.ReviewRequests {
		out.Reviewers = append(out.Reviewers, cmp.Or(r.Login, r.Name))
	}

	for _, u := range p.ReviewersGL {
		out.Reviewers = append(out.Reviewers, u.name())
	}

	for _, r := range p.Reviews {
		out.Reviews = append(
			out.Reviews,
			Review{Author: r.Author.name(), State: reviewState(r.State), At: r.SubmittedAt},
		)
	}

	return out
}

// decision reads gh's reviewDecision; glab has none.
func decision(s string) string {
	switch strings.ToUpper(s) {
	case "APPROVED":
		return Approved
	case "CHANGES_REQUESTED":
		return ChangesRequested
	case "REVIEW_REQUIRED":
		return ReviewRequired
	default:
		return ""
	}
}

func reviewState(s string) string {
	switch strings.ToUpper(s) {
	case "APPROVED":
		return Approved
	case "CHANGES_REQUESTED":
		return ChangesRequested
	case "COMMENTED":
		return Commented
	default:
		return ""
	}
}

// mergeable reads gh's two fields or glab's one into Clean, Conflicts,
// Blocked or "" for unknown.
func mergeable(gh, ghState, gl string) string {
	switch strings.ToUpper(gh) {
	case "CONFLICTING":
		return Conflicts
	case "MERGEABLE":
		switch strings.ToUpper(ghState) {
		case "CLEAN", "HAS_HOOKS", "UNSTABLE":
			return Clean
		case "DIRTY":
			return Conflicts
		case "BLOCKED", "BEHIND", "DRAFT":
			return Blocked
		default:
			return Clean
		}
	}

	switch strings.ToLower(gl) {
	case "mergeable":
		return Clean
	case "conflict", "need_rebase":
		return Conflicts
	case "":
		return ""
	default:
		return Blocked
	}
}

// checks is the rollup of a request's checks: gh lists each run and
// status context, glab the head pipeline.
func (p pull) checks() string {
	if pl := cmp.Or(p.HeadPipeline, p.Pipeline); pl != nil {
		switch strings.ToLower(pl.Status) {
		case "success":
			return Green
		case "failed", "canceled":
			return Red
		case "":
			return ""
		default:
			return Pending
		}
	}

	out := ""

	for _, c := range p.StatusCheckRollup {
		state := strings.ToUpper(cmp.Or(c.Conclusion, c.State))

		switch {
		case c.Status != "" && !strings.EqualFold(c.Status, "COMPLETED"), state == "PENDING", state == "":
			if out != Red {
				out = Pending
			}
		case state == "FAILURE",
			state == "ERROR",
			state == "TIMED_OUT",
			state == "CANCELLED",
			state == "STARTUP_FAILURE":
			out = Red
		default:
			if out == "" {
				out = Green
			}
		}
	}

	return out
}

// Pulls is every request a person's standup needs: their own, with their
// commits and force-pushes, and others' they reviewed or merged since
// since.
func (h Host) Pulls(ctx context.Context, login string, since time.Time) ([]PR, error) {
	if h.GLab() {
		return h.standupMRs(ctx, login, since)
	}

	stamp := since.UTC().Format(time.RFC3339)

	own, err := h.SearchPRs(ctx, "author:"+login+" updated:>="+stamp, OwnFields)
	if err != nil {
		return nil, err
	}

	reviewed, err := h.SearchPRs(ctx, "reviewed-by:"+login+" updated:>="+stamp, ForeignFields+",reviews")
	if err != nil {
		return nil, err
	}

	merged, err := h.SearchPRs(ctx, "is:merged merged:>="+stamp, ForeignFields)
	if err != nil {
		return nil, err
	}

	out := make([]PR, 0, len(own))

	for _, p := range own {
		if p.Commits, err = h.PullCommits(ctx, p.Number); err != nil {
			return nil, err
		}

		if p.ForcePushes, err = h.PRForcePushes(ctx, p.Number, login); err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return append(out, foreignPulls(login, reviewed, merged)...), nil
}

// PullsSince is what a board reads: every open request, and every one
// merged since since, each with the board's fields and no commits.
func (h Host) PullsSince(ctx context.Context, since time.Time) ([]PR, error) {
	if h.GLab() {
		open, err := h.listMRs(ctx, url.Values{"state": {"opened"}, "per_page": {"100"}})
		if err != nil {
			return nil, err
		}

		merged, err := h.listMRs(ctx, url.Values{
			"state": {"merged"}, "updated_after": {since.UTC().Format(time.RFC3339)}, "per_page": {"100"},
		})
		if err != nil {
			return nil, err
		}

		return append(open, slices.DeleteFunc(merged, func(p PR) bool { return p.MergedAt.Before(since) })...), nil
	}

	open, err := h.SearchPRs(ctx, "is:open", BoardFields)
	if err != nil {
		return nil, err
	}

	merged, err := h.SearchPRs(ctx, "is:merged merged:>="+since.UTC().Format(time.RFC3339), BoardFields)
	if err != nil {
		return nil, err
	}

	return append(open, merged...), nil
}

// HeadFields are what the janitor reads of a branch's requests.
const HeadFields = "number,state,headRefName,headRefOid,baseRefName,url,author,createdAt,mergedAt,closedAt"

// PullsByHead is every request whose head is branch, in any state, newest
// first. A branch can carry several: one closed, then another opened.
func (h Host) PullsByHead(ctx context.Context, branch string) ([]PR, error) {
	var out []PR

	if h.GLab() {
		var err error
		if out, err = h.listMRs(ctx, url.Values{
			"source_branch": {branch}, "state": {"all"}, "per_page": {"100"},
		}); err != nil {
			return nil, err
		}
	} else {
		var found []pull
		if err := h.Decode(ctx, &found, "pr", "list", "--head", branch, "--state", "all",
			"--json", HeadFields, "--limit", "100"); err != nil {
			return nil, err
		}

		for _, p := range found {
			out = append(out, p.pr())
		}
	}

	slices.SortStableFunc(out, func(a, b PR) int { return b.CreatedAt.Compare(a.CreatedAt) })

	return out, nil
}

// DefaultBranchName is the repo's default branch as the host has it.
func (h Host) DefaultBranchName(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranchRef struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
		DefaultBranch string `json:"default_branch"`
	}

	err := h.Decode(ctx, &repo, h.Pick(
		[]string{"repo", "view", "--json", "defaultBranchRef"},
		[]string{"repo", "view", "-F", "json"},
	)...)
	if err != nil {
		return "", err
	}

	name := cmp.Or(repo.DefaultBranchRef.Name, repo.DefaultBranch)
	if name == "" {
		return "", fmt.Errorf("%s returned no default branch", h.Name)
	}

	return name, nil
}

// PullCommits is the commits a request lists, one call per request: asked
// for inside the list query, gh's GraphQL refuses a node count past its
// limit.
func (h Host) PullCommits(ctx context.Context, number int) ([]PRCommit, error) {
	path := h.Pick(
		[]string{fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/commits?per_page=100", number)},
		[]string{fmt.Sprintf("projects/:fullpath/merge_requests/%d/commits?per_page=100", number)},
	)[0]

	commits, err := DecodePages[struct {
		SHA     string `json:"sha"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Email string    `json:"email"`
				Date  time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`

		ID           string    `json:"id"`
		Title        string    `json:"title"`
		Message      string    `json:"message"`
		AuthorEmail  string    `json:"author_email"`
		AuthoredDate time.Time `json:"authored_date"`
		ParentIDs    []string  `json:"parent_ids"`
	}](ctx, h, path)
	if err != nil {
		return nil, err
	}

	out := make([]PRCommit, 0, len(commits))

	for _, c := range commits {
		message := cmp.Or(c.Commit.Message, c.Message, c.Title)
		subject, _, _ := strings.Cut(message, "\n")
		out = append(out, PRCommit{
			SHA: cmp.Or(
				c.SHA,
				c.ID,
			),
			Subject: subject,
			Message: message,
			Email:   cmp.Or(c.Commit.Author.Email, c.AuthorEmail),
			At:      cmp.Or(c.Commit.Author.Date, c.AuthoredDate),
			Parents: max(len(c.Parents), len(c.ParentIDs)),
		})
	}

	return out, nil
}

// SearchPRs is one `gh pr list --search` with the fields named.
func (h Host) SearchPRs(ctx context.Context, query, fields string) ([]PR, error) {
	var found []pull

	err := h.Decode(ctx, &found, "pr", "list", "--state", "all", "--search", query,
		"--json", fields, "--limit", strconv.Itoa(PRLimit))
	if err != nil {
		return nil, err
	}

	if len(found) >= PRLimit {
		return nil, fmt.Errorf("gh found %d or more pull requests for %q: use a later --since", PRLimit, query)
	}

	out := make([]PR, 0, len(found))
	for _, p := range found {
		out = append(out, p.pr())
	}

	return out, nil
}

// foreignPulls merges the reviewed and merged lists into one entry per
// request, the user's own dropped: their own query read those in full.
func foreignPulls(login string, reviewed, merged []PR) []PR {
	var out []PR

	index := map[int]int{}

	for _, p := range append(reviewed, merged...) {
		if SameLogin(login, p.Author) {
			continue
		}

		if i, ok := index[p.Number]; ok {
			out[i].Reviews = append(out[i].Reviews, p.Reviews...)
			out[i].MergedBy = cmp.Or(out[i].MergedBy, p.MergedBy)
			out[i].MergedAt = cmp.Or(out[i].MergedAt, p.MergedAt)

			continue
		}

		index[p.Number] = len(out)
		out = append(out, p)
	}

	return out
}

// listMRs is one page walk of the MR list API with the query given.
func (h Host) listMRs(ctx context.Context, q url.Values) ([]PR, error) {
	found, err := DecodePages[pull](ctx, h, "projects/:fullpath/merge_requests?"+q.Encode())
	if err != nil {
		return nil, err
	}

	out := make([]PR, 0, len(found))
	for _, p := range found {
		out = append(out, p.pr())
	}

	return out, nil
}

// standupMRs is Pulls on GitLab: the MR list API filters by author, reviewer
// and update time where glab mr list cannot. A reviewer's part comes from the
// notes, which carry the time the list's reviewers field lacks.
func (h Host) standupMRs(ctx context.Context, login string, since time.Time) ([]PR, error) {
	list := func(filter string) ([]PR, error) {
		q := url.Values{"state": {"all"}, "updated_after": {since.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
		k, v, _ := strings.Cut(filter, "=")
		q.Set(k, v)

		return h.listMRs(ctx, q)
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

	out := make([]PR, 0, len(own))

	for _, p := range own {
		if p.Commits, err = h.PullCommits(ctx, p.Number); err != nil {
			return nil, err
		}

		p.Empty = len(p.Commits) == 0

		if p.ForcePushes, err = h.MRForcePushes(ctx, p.Number, since); err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	foreign := foreignPulls(login, asReviewer, merged)

	for i, p := range foreign {
		if !slices.ContainsFunc(asReviewer, func(r PR) bool { return r.Number == p.Number }) {
			continue
		}

		reviews, err := h.MRReviews(ctx, p.Number)
		if err != nil {
			return nil, err
		}

		foreign[i].Reviews = reviews
	}

	return append(out, foreign...), nil
}

// MRReviews is the notes that count as reviewing: any plain comment, and
// the system note an approval leaves.
func (h Host) MRReviews(ctx context.Context, iid int) ([]Review, error) {
	type note struct {
		Body      string    `json:"body"`
		System    bool      `json:"system"`
		CreatedAt time.Time `json:"created_at"`
		Author    user      `json:"author"`
	}

	notes, err := DecodePages[note](ctx, h, fmt.Sprintf("projects/:fullpath/merge_requests/%d/notes?per_page=100", iid))
	if err != nil {
		return nil, err
	}

	var out []Review

	for _, n := range notes {
		if n.System && !strings.HasPrefix(n.Body, "approved this merge request") {
			continue
		}

		state := Commented
		if n.System {
			state = Approved
		}

		out = append(out, Review{Author: n.Author.name(), State: state, At: n.CreatedAt})
	}

	return out, nil
}

// ForcePushesQuery is the GraphQL that lists a PR's force-pushes.
const ForcePushesQuery = "query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) " +
	"{ pullRequest(number: $number) { timelineItems(last: 100, itemTypes: [HEAD_REF_FORCE_PUSHED_EVENT]) " +
	"{ nodes { ... on HeadRefForcePushedEvent { createdAt actor { login } beforeCommit { oid } afterCommit { oid } } } } } } }"

// PRForcePushes is the force-pushes the user made to a PR, the last 100:
// GitHub records each with the head before and after.
func (h Host) PRForcePushes(ctx context.Context, number int, login string) ([]ForcePush, error) {
	var payload struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					TimelineItems struct {
						Nodes []struct {
							CreatedAt    time.Time `json:"createdAt"`
							Actor        user      `json:"actor"`
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

	err := h.Decode(ctx, &payload, "api", "graphql", "-f", "query="+ForcePushesQuery,
		"-F", "owner={owner}", "-F", "name={repo}", "-F", "number="+strconv.Itoa(number))
	if err != nil {
		return nil, err
	}

	var out []ForcePush

	for _, n := range payload.Data.Repository.PullRequest.TimelineItems.Nodes {
		if !SameLogin(login, n.Actor.name()) {
			continue
		}

		out = append(out, ForcePush{At: n.CreatedAt, Before: n.BeforeCommit.OID, After: n.AfterCommit.OID})
	}

	return out, nil
}

// MRForcePushes reads GitLab's MR versions, one per push, since the time:
// a version whose previous head is not its ancestor replaced commits. GitLab
// records no pusher, so on the user's own MR each counts as theirs.
func (h Host) MRForcePushes(ctx context.Context, iid int, since time.Time) ([]ForcePush, error) {
	type version struct {
		Head      string    `json:"head_commit_sha"`
		CreatedAt time.Time `json:"created_at"`
	}

	versions, err := DecodePages[version](
		ctx,
		h,
		fmt.Sprintf("projects/:fullpath/merge_requests/%d/versions?per_page=100", iid),
	)
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(versions, func(a, b version) int { return a.CreatedAt.Compare(b.CreatedAt) })

	var out []ForcePush

	for i := 1; i < len(versions); i++ {
		prev, cur := versions[i-1], versions[i]
		if cur.CreatedAt.Before(since) {
			continue
		}

		// display only: a head git cannot place lists as a force push
		if ff, _ := gitx.IsAncestor(ctx, h.Dir, prev.Head, cur.Head); ff {
			continue
		}

		out = append(out, ForcePush{At: cur.CreatedAt, Before: prev.Head, After: cur.Head})
	}

	return out, nil
}
