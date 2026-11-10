package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// The standup collectors are exercised against a scripted git and gh or
// glab: what each asks and what it reads back. Grouping is internal/standup's
// and tested there.

var standupSince = time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)

const (
	sinceArg = "--since=2026-10-14T00:00:00Z"
	ghStamp  = "2026-10-14T00:00:00Z"
)

func TestParseSince(t *testing.T) {
	if got, err := parseSince(
		"2026-10-14T09:30:00+03:00",
	); err != nil ||
		!got.Equal(time.Date(2026, 10, 14, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("rfc3339 = %v, %v", got, err)
	}

	if got, err := parseSince("2026-10-14"); err != nil || got != time.Date(2026, 10, 14, 0, 0, 0, 0, time.Local) {
		t.Errorf("date = %v, %v, want local midnight", got, err)
	}

	for _, bad := range []string{"2d", "yesterday", "", "2026-10-14 09:30", "1760000000"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) took it; the period's forms are not --since's", bad)
		}
	}
}

// Only the user's email counts, matched exactly and in any case: a commit
// by someone whose name contains the user's is not theirs. A commit the
// default branch reaches is on it whatever ref --source named.
func ghForcePushes(number int) string {
	return "gh api graphql -f query=" + forge.ForcePushesQuery + " -F owner={owner} -F name={repo} -F number=" + strconv.Itoa(
		number,
	)
}

func ghSearch(query, fields string) string {
	return "gh pr list --state all --search " + query + " --json " + fields + " --limit 1000"
}

// On GitHub: own PRs with their commits fetched one request at a time,
// others' reviewed and merged PRs merged into one entry each, the user's
// own dropped from those lists.
func TestStandupPullsGitHub(t *testing.T) {
	f := newFakes(t)

	f.script(ghSearch("author:me updated:>="+ghStamp, forge.OwnFields), `[
	  {"number":5,"title":"FT-2: b","state":"OPEN","isDraft":true,"headRefName":"FT-2/b","baseRefName":"FT-1/a",
	   "url":"u5","author":{"login":"me"},"createdAt":"2026-10-14T08:00:00Z","changedFiles":0,"mergeCommit":null}]`)
	f.script(
		"gh api --paginate repos/{owner}/{repo}/pulls/5/commits?per_page=100",
		`[{"sha":"a1","commit":{"message":"work on b\n\nbody","author":{"date":"2026-10-14T07:00:00Z"}}},{"sha":"a2"}]`,
	)
	f.script(ghForcePushes(5), `{"data":{"repository":{"pullRequest":{"timelineItems":{"nodes":[
	  {"createdAt":"2026-10-14T09:00:00Z","actor":{"login":"me"},"beforeCommit":{"oid":"b0"},"afterCommit":{"oid":"b1"}},
	  {"createdAt":"2026-10-14T10:00:00Z","actor":{"login":"bob"},"beforeCommit":{"oid":"b1"},"afterCommit":{"oid":"b2"}}]}}}}}`)
	f.script(ghSearch("reviewed-by:me updated:>="+ghStamp, forge.ForeignFields+",reviews"), `[
	  {"number":9,"title":"theirs","state":"MERGED","url":"u9","author":{"login":"bob"},
	   "reviews":[{"author":{"login":"me"},"submittedAt":"2026-10-14T09:00:00Z"}]},
	  {"number":5,"title":"FT-2: b","state":"OPEN","url":"u5","author":{"login":"me"},"reviews":[]}]`)
	f.script(ghSearch("is:merged merged:>="+ghStamp, forge.ForeignFields), `[
	  {"number":9,"title":"theirs","state":"MERGED","url":"u9","author":{"login":"bob"},
	   "mergedAt":"2026-10-14T10:00:00Z","mergedBy":{"login":"me"}}]`)

	got, err := newHost("gh", ".").Pulls(t.Context(), "me", standupSince)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d PRs, want own #5 and foreign #9: %+v", len(got), got)
	}

	own, foreign := got[0], got[1]

	if own.Number != 5 || own.Status != standup.HostOpen || !own.Draft || !own.Empty ||
		own.Head != "FT-2/b" || own.Base != "FT-1/a" || len(own.Commits) != 2 || own.Commits[0].Subject != "work on b" || own.Commits[1].SHA != "a2" {
		t.Errorf("own = %+v", own)
	}

	if len(own.ForcePushes) != 1 || own.ForcePushes[0].Before != "b0" || own.ForcePushes[0].After != "b1" {
		t.Errorf("force-pushes = %+v, want only mine, b0 to b1", own.ForcePushes)
	}

	if foreign.Number != 9 || foreign.MergedBy != "me" || len(foreign.Reviews) != 1 ||
		foreign.Status != standup.HostMerged {
		t.Errorf("foreign = %+v, want #9 reviewed and merged by me", foreign)
	}
}

// On GitLab: the MR list API three ways, commits for own MRs, notes for
// the MRs the user is a reviewer on, an approval's system note counting.
func TestStandupPullsGitLab(t *testing.T) {
	f := newFakes(t)

	mrs := "glab api --paginate projects/:fullpath/merge_requests?"
	q := "&state=all&updated_after=" + strings.ReplaceAll(ghStamp, ":", "%3A")

	f.script(mrs+"author_username=me&per_page=100"+q, `[{"iid":3,"title":"FT-2 b","state":"opened","draft":false,
	  "source_branch":"FT-2/b","target_branch":"main","web_url":"w3","author":{"username":"me"},
	  "created_at":"2026-10-14T08:00:00Z","merge_commit_sha":null}]`)
	f.script(mrs+"per_page=100&reviewer_username=me"+q, `[{"iid":7,"title":"theirs","state":"opened",
	  "author":{"username":"bob"},"web_url":"w7"}]`)
	f.script(mrs+"per_page=100&state=merged&updated_after="+strings.ReplaceAll(ghStamp, ":", "%3A"), `[]`)
	f.script(
		"glab api --paginate projects/:fullpath/merge_requests/3/commits?per_page=100",
		`[{"id":"c1","title":"b","authored_date":"2026-10-14T07:00:00Z"}]`,
	)
	// newest first, as GitLab lists them: v3 replaced v2's commits, v2
	// built on v1, and v1 predates the period
	f.script("glab api --paginate projects/:fullpath/merge_requests/3/versions?per_page=100", `[
	  {"head_commit_sha":"v3","created_at":"2026-10-14T11:00:00Z"},
	  {"head_commit_sha":"v2","created_at":"2026-10-14T09:00:00Z"},
	  {"head_commit_sha":"v1","created_at":"2026-10-13T09:00:00Z"}]`)
	f.script("git merge-base --is-ancestor v1 v2", "")
	f.fail("git merge-base --is-ancestor v2 v3", shell.Result{Code: 1})
	f.script("glab api --paginate projects/:fullpath/merge_requests/7/notes?per_page=100", `[
	  {"body":"approved this merge request","system":true,"created_at":"2026-10-14T09:00:00Z","author":{"username":"me"}},
	  {"body":"requested review from @me","system":true,"created_at":"2026-10-14T08:00:00Z","author":{"username":"bob"}},
	  {"body":"looks good","system":false,"created_at":"2026-10-14T10:00:00Z","author":{"username":"me"}}]`)

	got, err := newHost("glab", ".").Pulls(t.Context(), "me", standupSince)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d MRs, want 2: %+v", len(got), got)
	}

	if own := got[0]; own.Number != 3 || own.Empty || own.Head != "FT-2/b" ||
		len(own.Commits) != 1 || own.Commits[0].SHA != "c1" || own.Commits[0].Subject != "b" {
		t.Errorf("own = %+v", own)
	}

	if fp := got[0].ForcePushes; len(fp) != 1 || fp[0].Before != "v2" || fp[0].After != "v3" {
		t.Errorf("force-pushes = %+v, want v2 to v3 only", fp)
	}

	if foreign := got[1]; foreign.Number != 7 || len(foreign.Reviews) != 2 {
		t.Errorf("foreign = %+v, want the approval and the comment", foreign)
	}
}

func TestRenderStandup(t *testing.T) {
	merged := standupSince.Add(5 * time.Hour)
	r := standup.Standup{
		Since:  standupSince,
		Period: standup.PeriodLastStandup,
		Days:   []string{"2026-10-09", "2026-10-12"},
		Projects: []standup.Project{{
			Name:  "f10",
			Repos: []standup.Repo{{Name: "f10"}},
			Tasks: []standup.Task{{ID: "FT-1", State: standup.StateInReview, PRs: []standup.PR{
				{Number: 1, MergedAt: &merged, Commits: []standup.Commit{{SHA: "a"}}}, {Number: 2, BasePR: 1},
			}}},
			Other: standup.Other{Commits: []standup.Commit{{SHA: "x"}}},
			Reviewed: []standup.ReviewedPR{
				{
					Number:  9,
					Title:   "theirs",
					Author:  "bob",
					Actions: []standup.Action{{Kind: "reviewed"}, {Kind: "merged"}},
				},
			},
		}},
	}

	var out bytes.Buffer
	if err := renderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"(last standup)", "Fri 10-09, Mon 10-12", "FT-1", "in review", "#1 -> #2", "1 commit(s)", "#9 theirs (bob): reviewed, merged"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}

// scriptStandupGit answers the git reads collectStandup makes, with one
// commit of the user's on FT-2/b and origin/HEAD as given.
// A host CLI that fails is the command's error, never commits only.
// Without origin/HEAD the host names the default branch; without a host
// either, standup refuses rather than read landed commits as unfinished.
// Upkeep-only tasks leave the per-task lines for one line of their own.
func TestRenderStandupFoldsUpkeep(t *testing.T) {
	r := standup.Standup{Since: standupSince, Projects: []standup.Project{{Tasks: []standup.Task{
		{ID: "FT-1", State: standup.StateInReview, Class: standup.ClassUpkeep},
		{ID: "FT-2", State: standup.StateInProgress, Class: standup.ClassWork},
		{ID: "FT-3", State: standup.StateInReview, Class: standup.ClassUpkeep},
	}}}}

	var out bytes.Buffer
	if err := renderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "kept up to date  FT-1, FT-3\n") || strings.Contains(got, "FT-1      ") ||
		!strings.Contains(got, "FT-2") {
		t.Errorf("render:\n%s", got)
	}
}

// commitFacts reads numstat, the generated marks and base merges once
// for all commits, and keys each to its commit.
// A merge on a stacked branch checks the recorded base before main.
func TestCommitCI(t *testing.T) {
	f := newFakes(t)
	f.script("gh api repos/{owner}/{repo}/commits/p1/check-runs?per_page=100", `{"check_runs":[
	  {"conclusion":"success","completed_at":"2026-10-14T09:00:00Z"},
	  {"conclusion":"failure","completed_at":"2026-10-14T10:00:00Z"},
	  {"conclusion":null,"completed_at":null}]}`)
	f.script("gh api repos/{owner}/{repo}/commits/p1/status", `{"statuses":[]}`)
	f.script("gh api repos/{owner}/{repo}/commits/p2/check-runs?per_page=100", `{"check_runs":[]}`)
	f.script("gh api repos/{owner}/{repo}/commits/p2/status", `{"statuses":[]}`)
	f.script("glab api projects/:fullpath/repository/commits/p3",
		`{"last_pipeline":{"status":"failed","updated_at":"2026-10-14T11:00:00Z"}}`)

	gh := newHost("gh", ".")

	ci, err := gh.CommitCI(t.Context(), "p1")
	if err != nil || ci == nil || !ci.Red || !ci.At.Equal(standupSince.Add(10*time.Hour)) {
		t.Errorf("p1 = %+v, %v; want red at 10:00", ci, err)
	}

	if ci, err := gh.CommitCI(t.Context(), "p2"); err != nil || ci != nil {
		t.Errorf("p2 = %+v, %v; want unknown with no checks", ci, err)
	}

	if ci, err := newHost("glab", ".").CommitCI(t.Context(), "p3"); err != nil || ci == nil || !ci.Red {
		t.Errorf("p3 = %+v, %v; want a red pipeline", ci, err)
	}
}

// A merge made on the default branch merges into it: unpushed, its second
// parent is origin's tip, and it must still not read as a base merge.
// A CI read that fails, as for a parent the host never saw, leaves the
// commit's CI unknown instead of failing the report; commits from before
// the period are never asked about.
// Several projects each get a heading; a project over several repositories
// names each PR with its repository.
func TestRenderStandupProjects(t *testing.T) {
	r := standup.Standup{Since: standupSince, Projects: []standup.Project{
		{
			Name:  "ACME",
			Repos: []standup.Repo{{Name: "svc-a"}, {Name: "svc-b"}},
			Tasks: []standup.Task{{ID: "ACME-1", State: standup.StateInReview, PRs: []standup.PR{
				{Repo: "svc-a", Number: 12}, {Repo: "svc-b", Number: 40}, {Repo: "svc-b", Number: 41, BasePR: 40},
			}}},
		},
		{
			Name:  "r3",
			Repos: []standup.Repo{{Name: "r3"}},
			Tasks: []standup.Task{
				{ID: "R3-2", State: standup.StateShipped, PRs: []standup.PR{{Repo: "r3", Number: 5}}},
			},
		},
	}}

	var out bytes.Buffer
	if err := renderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"== ACME (svc-a, svc-b)\n", "svc-a#12, svc-b#40 -> svc-b#41", "== r3\n", "R3-2       shipped      #5"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}

// An arrow means a PR sits on the one before it: siblings on one root and
// unrelated PRs in other are listed, not chained.
func TestRenderStandupChainsOnlyStackedPRs(t *testing.T) {
	r := standup.Standup{Since: standupSince, Projects: []standup.Project{{
		Tasks: []standup.Task{{ID: "FT-1", State: standup.StateInReview, PRs: []standup.PR{
			{Number: 1}, {Number: 2, BasePR: 1}, {Number: 3, BasePR: 1},
		}}},
		Other: standup.Other{PRs: []standup.PR{{Number: 5}, {Number: 9, BasePR: 3}}},
	}}}

	var out bytes.Buffer
	if err := renderStandup(&out, nil, r); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"#1 -> #2, #3", "other      #5, #9"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}
