package forge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What happened to a request in a window, one kind per event. Opened is
// read off the request's creation time; the rest are timeline events.
const (
	EventOpened          = "opened"
	EventReady           = "ready" // out of draft
	EventCommit          = "commit"
	EventMergeFromBase   = "merge-from-base" // a commit with more than one parent
	EventForcePush       = "force-push"
	EventReview          = "review"
	EventReviewRequested = "review-requested"
	EventMerged          = "merged"
	EventClosed          = "closed"
)

// An Event is one thing that happened to a request.
type Event struct {
	Kind    string
	At      time.Time
	Actor   string
	Message string // a commit's whole message, trailers included
	State   string // a review's: Approved, ChangesRequested or Commented
}

// A Touched is a request updated in the window, with what happened to it
// there.
type Touched struct {
	PR

	Events []Event
}

// Activity is what a repository did since a time: every request touched,
// and how many open ones nobody touched, with the oldest of them.
type Activity struct {
	Requests    []Touched
	QuietCount  int
	QuietOldest time.Time // the oldest quiet request's creation time
}

// ActivityQuery is the GraphQL a GitHub feed reads: requests updated since
// the window's start with their timeline, and the quiet open ones counted.
// A timeline keeps its newest 100 events, which a request rarely passes.
const ActivityQuery = `query($q: String!, $quiet: String!, $since: DateTime!, $endCursor: String) {
  search(query: $q, type: ISSUE, first: 50, after: $endCursor) {
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest {
      number title url state isDraft headRefName baseRefName createdAt updatedAt mergedAt closedAt body
      author { login } mergedBy { login } assignees(first: 10) { nodes { login } }
      timelineItems(since: $since, last: 100, itemTypes: [PULL_REQUEST_COMMIT, HEAD_REF_FORCE_PUSHED_EVENT,
        PULL_REQUEST_REVIEW, READY_FOR_REVIEW_EVENT, REVIEW_REQUESTED_EVENT, MERGED_EVENT, CLOSED_EVENT]) {
        nodes {
          __typename
          ... on PullRequestCommit { commit { committedDate message parents { totalCount } author { user { login } } } }
          ... on HeadRefForcePushedEvent { createdAt actor { login } }
          ... on PullRequestReview { state submittedAt author { login } }
          ... on ReadyForReviewEvent { createdAt actor { login } }
          ... on ReviewRequestedEvent { createdAt requestedReviewer { ... on User { login } ... on Team { name } } }
          ... on MergedEvent { createdAt actor { login } }
          ... on ClosedEvent { createdAt actor { login } }
        }
      }
    } }
  }
  quiet: search(query: $quiet, type: ISSUE, first: 1) { issueCount nodes { ... on PullRequest { createdAt } } }
}`

// Activity reads what the repository did since since: one paged query on
// GitHub, on GitLab a list of the updated merge requests read one by one.
func (h Host) Activity(ctx context.Context, since time.Time) (Activity, error) {
	if h.GLab() {
		return h.mrActivity(ctx, since)
	}

	at := since.UTC().Format(time.RFC3339)

	out, err := h.Run(ctx, "api", "graphql", "--paginate", "-f", "query="+ActivityQuery,
		"-F", "q=repo:{owner}/{repo} is:pr updated:>="+at,
		"-F", "quiet=repo:{owner}/{repo} is:pr is:open updated:<"+at+" sort:created-asc",
		"-F", "since="+at)
	if err != nil {
		return Activity{}, err
	}

	var a Activity

	dec := json.NewDecoder(strings.NewReader(out))

	for page := 0; ; page++ {
		var doc activityPage

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return a, nil
		}

		if err != nil {
			return Activity{}, fmt.Errorf("parsing %s output: %w", h.Name, err)
		}

		if page == 0 {
			a.QuietCount = doc.Data.Quiet.IssueCount
			if len(doc.Data.Quiet.Nodes) > 0 {
				a.QuietOldest = doc.Data.Quiet.Nodes[0].CreatedAt
			}
		}

		for _, n := range doc.Data.Search.Nodes {
			if n.Number == 0 {
				continue // an issue the search matched by accident
			}

			a.Requests = append(a.Requests, n.touched())
		}
	}
}

// activityPage is one page of ActivityQuery's answer.
type activityPage struct {
	Data struct {
		Search struct {
			Nodes []activityNode `json:"nodes"`
		} `json:"search"`
		Quiet struct {
			IssueCount int `json:"issueCount"`
			Nodes      []struct {
				CreatedAt time.Time `json:"createdAt"`
			} `json:"nodes"`
		} `json:"quiet"`
	} `json:"data"`
}

type activityNode struct {
	pull

	AssigneeNodes struct {
		Nodes []user `json:"nodes"`
	} `json:"assignees"`

	Timeline struct {
		Nodes []struct {
			Type   string `json:"__typename"`
			Commit struct {
				CommittedDate time.Time `json:"committedDate"`
				Message       string    `json:"message"`
				Parents       struct {
					TotalCount int `json:"totalCount"`
				} `json:"parents"`
				Author struct {
					User *user `json:"user"`
				} `json:"author"`
			} `json:"commit"`
			CreatedAt   time.Time `json:"createdAt"`
			SubmittedAt time.Time `json:"submittedAt"`
			Actor       *user     `json:"actor"`
			Author      *user     `json:"author"`
			State       string    `json:"state"`
			Requested   *struct {
				Login string `json:"login"`
				Name  string `json:"name"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"timelineItems"`
}

func (n activityNode) touched() Touched {
	// assignees come as a connection here, not the list pr() reads
	n.Assignees = n.AssigneeNodes.Nodes
	t := Touched{PR: n.pr()}

	who := func(u *user) string {
		if u == nil {
			return ""
		}

		return u.name()
	}

	for _, e := range n.Timeline.Nodes {
		switch e.Type {
		case "PullRequestCommit":
			kind := EventCommit
			if e.Commit.Parents.TotalCount > 1 {
				kind = EventMergeFromBase
			}

			t.Events = append(t.Events, Event{
				Kind: kind, At: e.Commit.CommittedDate, Actor: who(e.Commit.Author.User), Message: e.Commit.Message,
			})
		case "HeadRefForcePushedEvent":
			t.Events = append(t.Events, Event{Kind: EventForcePush, At: e.CreatedAt, Actor: who(e.Actor)})
		case "PullRequestReview":
			t.Events = append(t.Events, Event{
				Kind: EventReview, At: e.SubmittedAt, Actor: who(e.Author), State: reviewState(e.State),
			})
		case "ReadyForReviewEvent":
			t.Events = append(t.Events, Event{Kind: EventReady, At: e.CreatedAt, Actor: who(e.Actor)})
		case "ReviewRequestedEvent":
			reviewer := ""
			if e.Requested != nil {
				reviewer = cmp.Or(e.Requested.Login, e.Requested.Name)
			}

			t.Events = append(t.Events, Event{Kind: EventReviewRequested, At: e.CreatedAt, Actor: reviewer})
		case "MergedEvent":
			t.Events = append(t.Events, Event{Kind: EventMerged, At: e.CreatedAt, Actor: who(e.Actor)})
		case "ClosedEvent":
			t.Events = append(t.Events, Event{Kind: EventClosed, At: e.CreatedAt, Actor: who(e.Actor)})
		}
	}

	t.Events = append(t.Events, Event{Kind: EventOpened, At: t.CreatedAt, Actor: t.Author})

	return t
}

// mrActivity is Activity on GitLab: the list API filters by update time,
// and only the merge requests it returns are read further.
func (h Host) mrActivity(ctx context.Context, since time.Time) (Activity, error) {
	at := since.UTC().Format(time.RFC3339)

	touched, err := h.listMRs(ctx, url.Values{"state": {"all"}, "updated_after": {at}, "per_page": {"100"}})
	if err != nil {
		return Activity{}, err
	}

	quiet, err := h.listMRs(ctx, url.Values{
		"state": {"opened"}, "updated_before": {at}, "order_by": {"created_at"}, "sort": {"asc"}, "per_page": {"100"},
	})
	if err != nil {
		return Activity{}, err
	}

	a := Activity{QuietCount: len(quiet)}
	if len(quiet) > 0 {
		a.QuietOldest = quiet[0].CreatedAt
	}

	a.Requests = make([]Touched, len(touched))
	errs := make([]error, len(touched))
	sem := make(chan struct{}, mrParallel)

	var wg sync.WaitGroup

	for i, p := range touched {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			a.Requests[i], errs[i] = h.mrTouched(ctx, p, since)
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		return Activity{}, err
	}

	return a, nil
}

// mrParallel bounds the reads in flight for one GitLab project's feed:
// three per merge request touched in the window.
const mrParallel = 8

// mrTouched is one merge request with its events: its commits, the notes
// that review it, and the force pushes among its versions.
func (h Host) mrTouched(ctx context.Context, p PR, since time.Time) (Touched, error) {
	t := Touched{PR: p}

	commits, err := h.PullCommits(ctx, p.Number)
	if err != nil {
		return Touched{}, err
	}

	for _, c := range commits {
		kind := EventCommit
		if c.Parents > 1 {
			kind = EventMergeFromBase
		}

		t.Events = append(t.Events, Event{Kind: kind, At: c.At, Actor: c.Email, Message: c.Message})
	}

	reviews, err := h.MRReviews(ctx, p.Number)
	if err != nil {
		return Touched{}, err
	}

	for _, r := range reviews {
		t.Events = append(t.Events, Event{Kind: EventReview, At: r.At, Actor: r.Author, State: r.State})
	}

	pushes, err := h.mrRewrites(ctx, p.Number, since, commits)
	if err != nil {
		return Touched{}, err
	}

	t.Events = append(t.Events, pushes...)
	t.Events = append(t.Events, Event{Kind: EventOpened, At: p.CreatedAt, Actor: p.Author})

	switch p.Status {
	case Merged:
		t.Events = append(t.Events, Event{Kind: EventMerged, At: p.MergedAt, Actor: p.MergedBy})
	case Closed:
		t.Events = append(t.Events, Event{Kind: EventClosed, At: p.ClosedAt})
	}

	return t, nil
}

// mrRewrites is the latest force push among a merge request's versions: a
// version whose head is among the request's commits while the one before it
// is not. The clone is never asked, since the feed may run with no checkout.
func (h Host) mrRewrites(ctx context.Context, iid int, since time.Time, commits []PRCommit) ([]Event, error) {
	type version struct {
		Head      string    `json:"head_commit_sha"`
		CreatedAt time.Time `json:"created_at"`
	}

	versions, err := DecodePages[version](
		ctx, h, fmt.Sprintf("projects/:fullpath/merge_requests/%d/versions?per_page=100", iid),
	)
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(versions, func(a, b version) int { return a.CreatedAt.Compare(b.CreatedAt) })

	kept := map[string]bool{}
	for _, c := range commits {
		kept[c.SHA] = true
	}

	var out []Event

	for i := 1; i < len(versions); i++ {
		if prev, cur := versions[i-1], versions[i]; !cur.CreatedAt.Before(since) && !kept[prev.Head] &&
			kept[cur.Head] {
			out = append(out, Event{Kind: EventForcePush, At: cur.CreatedAt})
		}
	}

	return out, nil
}

// EarlierRequests is every request that may name each id, to tell a task's
// first request from a follow-up: one GraphQL call for every id on GitHub,
// one list per id on GitLab. The searches only narrow; the caller matches.
func (h Host) EarlierRequests(ctx context.Context, ids []string) (map[string][]PR, error) {
	out := map[string][]PR{}
	if len(ids) == 0 {
		return out, nil
	}

	if h.GLab() {
		found := make([][]PR, len(ids))
		errs := make([]error, len(ids))
		sem := make(chan struct{}, mrParallel)

		var wg sync.WaitGroup

		for i, id := range ids {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()

				found[i], errs[i] = h.listMRs(ctx, url.Values{"state": {"all"}, "search": {id}, "per_page": {"100"}})
			})
		}

		wg.Wait()

		if err := errors.Join(errs...); err != nil {
			return nil, err
		}

		for i, id := range ids {
			out[id] = found[i]
		}

		return out, nil
	}

	var (
		decl   []string
		fields []string
		args   = []string{"api", "graphql"}
	)

	const nodes = "{ nodes { ... on PullRequest { number title url state headRefName createdAt mergedAt closedAt } } }"

	for i, id := range ids {
		for _, q := range []struct{ alias, search string }{{"t", id}, {"h", "head:" + id}} {
			v := q.alias + strconv.Itoa(i)
			decl = append(decl, "$"+v+": String!")
			fields = append(fields, fmt.Sprintf("%s: search(query: $%s, type: ISSUE, first: 50) %s", v, v, nodes))
			args = append(args, "-F", v+"=repo:{owner}/{repo} is:pr "+q.search)
		}
	}

	query := "query(" + strings.Join(decl, ", ") + ") { " + strings.Join(fields, " ") + " }"
	args = slices.Insert(args, 2, "-f", "query="+query)

	var payload struct {
		Data map[string]struct {
			Nodes []pull `json:"nodes"`
		} `json:"data"`
	}

	if err := h.Decode(ctx, &payload, args...); err != nil {
		return nil, err
	}

	for i, id := range ids {
		seen := map[int]bool{}

		for _, alias := range []string{"t", "h"} {
			for _, p := range payload.Data[alias+strconv.Itoa(i)].Nodes {
				if n := p.number(); n != 0 && !seen[n] {
					seen[n] = true
					out[id] = append(out[id], p.pr())
				}
			}
		}
	}

	return out, nil
}

// Branches is every branch the host has, one paged listing: what the
// board asks of every merged request at once.
func (h Host) Branches(ctx context.Context) (map[string]bool, error) {
	path := h.Pick(
		[]string{"repos/{owner}/{repo}/branches?per_page=100"},
		[]string{"projects/:fullpath/repository/branches?per_page=100"},
	)[0]

	found, err := DecodePages[struct {
		Name string `json:"name"`
	}](ctx, h, path)
	if err != nil {
		return nil, err
	}

	out := make(map[string]bool, len(found))
	for _, b := range found {
		out[b.Name] = true
	}

	return out, nil
}
