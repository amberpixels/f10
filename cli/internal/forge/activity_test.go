package forge

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The GitHub feed is one paged query: the quiet count from the first
// page, every request from every page, a merge commit read as a merge
// from the base, a review with its verdict, and opened from createdAt.
func TestActivityGitHub(t *testing.T) {
	since := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	at := "2026-10-23T00:00:00Z"
	key := "gh api graphql --paginate -f query=" + ActivityQuery +
		" -F q=repo:{owner}/{repo} is:pr updated:>=" + at +
		" -F quiet=repo:{owner}/{repo} is:pr is:open updated:<" + at + " sort:created-asc" +
		" -F since=" + at

	script(t, map[string]string{key: `{"data":{"search":{"nodes":[
	  {"number":7,"title":"FT-7: x","state":"OPEN","headRefName":"FT-7/x","createdAt":"2026-10-23T09:00:00Z",
	   "author":{"login":"ann"},"assignees":{"nodes":[{"login":"bob"}]},
	   "timelineItems":{"nodes":[
	     {"__typename":"PullRequestCommit","commit":{"committedDate":"2026-10-23T10:00:00Z","message":"a\n\nStep: implement",
	      "parents":{"totalCount":1},"author":{"user":{"login":"ann"}}}},
	     {"__typename":"PullRequestCommit","commit":{"committedDate":"2026-10-23T11:00:00Z","message":"Merge main",
	      "parents":{"totalCount":2},"author":{"user":null}}},
	     {"__typename":"PullRequestReview","state":"APPROVED","submittedAt":"2026-10-23T12:00:00Z","author":{"login":"bob"}},
	     {"__typename":"ReviewRequestedEvent","createdAt":"2026-10-23T09:30:00Z","requestedReviewer":{"name":"core"}}]}},
	  {}]},
	 "quiet":{"issueCount":12,"nodes":[{"createdAt":"2026-01-02T00:00:00Z"}]}}}
{"data":{"search":{"nodes":[{"number":8,"state":"MERGED","mergedAt":"2026-10-23T13:00:00Z","timelineItems":{"nodes":[
  {"__typename":"MergedEvent","createdAt":"2026-10-23T13:00:00Z","actor":{"login":"bob"}}]}}]},
 "quiet":{"issueCount":99,"nodes":[]}}}`})

	a, err := Host{Name: "gh"}.Activity(t.Context(), since)
	if err != nil {
		t.Fatal(err)
	}

	if a.QuietCount != 12 || !a.QuietOldest.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("quiet = %d, %v", a.QuietCount, a.QuietOldest)
	}

	if len(a.Requests) != 2 || a.Requests[0].Number != 7 || a.Requests[1].Status != Merged {
		t.Fatalf("requests = %+v", a.Requests)
	}

	first := a.Requests[0]
	if !slices.Equal(first.Assignees, []string{"bob"}) {
		t.Errorf("assignees = %v", first.Assignees)
	}

	var kinds []string
	for _, e := range first.Events {
		kinds = append(kinds, e.Kind)
	}

	want := []string{EventCommit, EventMergeFromBase, EventReview, EventReviewRequested, EventOpened}
	if !slices.Equal(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}

	if first.Events[2].State != Approved || first.Events[3].Actor != "core" ||
		!strings.Contains(first.Events[0].Message, "Step: implement") {
		t.Errorf("events = %+v", first.Events)
	}
}

// Earlier requests for every id are one query, each id its own alias.
func TestEarlierRequestsOneCall(t *testing.T) {
	query := "query($q0: String!, $q1: String!) { " +
		"t0: search(query: $q0, type: ISSUE, first: 50) { nodes { ... on PullRequest { number title url state headRefName createdAt mergedAt closedAt } } } " +
		"t1: search(query: $q1, type: ISSUE, first: 50) { nodes { ... on PullRequest { number title url state headRefName createdAt mergedAt closedAt } } } }"

	script(t, map[string]string{
		"gh api graphql -f query=" + query + " -F q0=repo:{owner}/{repo} is:pr FT-1 -F q1=repo:{owner}/{repo} is:pr FT-2": `{"data":{
		  "t0":{"nodes":[{"number":3,"state":"MERGED","headRefName":"FT-1/a"}]},"t1":{"nodes":[]}}}`,
	})

	got, err := Host{Name: "gh"}.EarlierRequests(t.Context(), []string{"FT-1", "FT-2"})
	if err != nil || len(got["FT-1"]) != 1 || got["FT-1"][0].Number != 3 || len(got["FT-2"]) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}

	if none, err := (Host{Name: "gh"}).EarlierRequests(t.Context(), nil); err != nil || len(none) != 0 {
		t.Errorf("no ids made a call: %v, %v", none, err)
	}
}

// Every branch is one paged listing.
func TestBranches(t *testing.T) {
	script(t, map[string]string{
		"gh api --paginate repos/{owner}/{repo}/branches?per_page=100": `[{"name":"main"},{"name":"FT-1/a"}]`,
	})

	got, err := Host{Name: "gh"}.Branches(t.Context())
	if err != nil || !got["FT-1/a"] || got["gone"] {
		t.Errorf("got %v, %v", got, err)
	}
}

// GitLab reads the merge requests updated since, then each one's commits,
// notes and versions, and counts the quiet ones from their own list.
func TestActivityGitLab(t *testing.T) {
	since := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

	script(t, map[string]string{
		"glab api --paginate projects/:fullpath/merge_requests?per_page=100&state=all&updated_after=2026-10-23T00%3A00%3A00Z": `[
		  {"iid":4,"state":"merged","source_branch":"FT-4/x","created_at":"2026-10-20T00:00:00Z","merged_at":"2026-10-23T08:00:00Z"}]`,
		"glab api --paginate projects/:fullpath/merge_requests?order_by=created_at&per_page=100&sort=asc&state=opened&updated_before=2026-10-23T00%3A00%3A00Z": `[
		  {"iid":1,"state":"opened","created_at":"2026-01-01T00:00:00Z"},{"iid":2,"state":"opened"}]`,
		"glab api --paginate projects/:fullpath/merge_requests/4/commits?per_page=100": `[
		  {"id":"a","message":"m","authored_date":"2026-10-23T07:00:00Z","parent_ids":["p1","p2"]}]`,
		"glab api --paginate projects/:fullpath/merge_requests/4/notes?per_page=100":    `[]`,
		"glab api --paginate projects/:fullpath/merge_requests/4/versions?per_page=100": `[]`,
	})

	a, err := Host{Name: "glab"}.Activity(t.Context(), since)
	if err != nil {
		t.Fatal(err)
	}

	if a.QuietCount != 2 || len(a.Requests) != 1 {
		t.Fatalf("activity = %+v", a)
	}

	var kinds []string
	for _, e := range a.Requests[0].Events {
		kinds = append(kinds, e.Kind)
	}

	if !slices.Equal(kinds, []string{EventMergeFromBase, EventOpened, EventMerged}) {
		t.Errorf("kinds = %v", kinds)
	}
}
