package hostops_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

const (
	sinceArg = "--since=2026-10-14T00:00:00Z"
	ghStamp  = "2026-10-14T00:00:00Z"
)

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
	f := testkit.NewFakes(t)

	f.Script(ghSearch("author:me updated:>="+ghStamp, forge.OwnFields), `[
	  {"number":5,"title":"FT-2: b","state":"OPEN","isDraft":true,"headRefName":"FT-2/b","baseRefName":"FT-1/a",
	   "url":"u5","author":{"login":"me"},"createdAt":"2026-10-14T08:00:00Z","changedFiles":0,"mergeCommit":null}]`)
	f.Script(
		"gh api --paginate repos/{owner}/{repo}/pulls/5/commits?per_page=100",
		`[{"sha":"a1","commit":{"message":"work on b\n\nbody","author":{"date":"2026-10-14T07:00:00Z"}}},{"sha":"a2"}]`,
	)
	f.Script(ghForcePushes(5), `{"data":{"repository":{"pullRequest":{"timelineItems":{"nodes":[
	  {"createdAt":"2026-10-14T09:00:00Z","actor":{"login":"me"},"beforeCommit":{"oid":"b0"},"afterCommit":{"oid":"b1"}},
	  {"createdAt":"2026-10-14T10:00:00Z","actor":{"login":"bob"},"beforeCommit":{"oid":"b1"},"afterCommit":{"oid":"b2"}}]}}}}}`)
	f.Script(ghSearch("reviewed-by:me updated:>="+ghStamp, forge.ForeignFields+",reviews"), `[
	  {"number":9,"title":"theirs","state":"MERGED","url":"u9","author":{"login":"bob"},
	   "reviews":[{"author":{"login":"me"},"submittedAt":"2026-10-14T09:00:00Z"}]},
	  {"number":5,"title":"FT-2: b","state":"OPEN","url":"u5","author":{"login":"me"},"reviews":[]}]`)
	f.Script(ghSearch("is:merged merged:>="+ghStamp, forge.ForeignFields), `[
	  {"number":9,"title":"theirs","state":"MERGED","url":"u9","author":{"login":"bob"},
	   "mergedAt":"2026-10-14T10:00:00Z","mergedBy":{"login":"me"}}]`)

	got, err := hostops.NewHost("gh", ".").Pulls(t.Context(), "me", testkit.StandupSince)
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
	f := testkit.NewFakes(t)

	mrs := "glab api --paginate projects/:fullpath/merge_requests?"
	q := "&state=all&updated_after=" + strings.ReplaceAll(ghStamp, ":", "%3A")

	f.Script(mrs+"author_username=me&per_page=100"+q, `[{"iid":3,"title":"FT-2 b","state":"opened","draft":false,
	  "source_branch":"FT-2/b","target_branch":"main","web_url":"w3","author":{"username":"me"},
	  "created_at":"2026-10-14T08:00:00Z","merge_commit_sha":null}]`)
	f.Script(mrs+"per_page=100&reviewer_username=me"+q, `[{"iid":7,"title":"theirs","state":"opened",
	  "author":{"username":"bob"},"web_url":"w7"}]`)
	f.Script(mrs+"per_page=100&state=merged&updated_after="+strings.ReplaceAll(ghStamp, ":", "%3A"), `[]`)
	f.Script(
		"glab api --paginate projects/:fullpath/merge_requests/3/commits?per_page=100",
		`[{"id":"c1","title":"b","authored_date":"2026-10-14T07:00:00Z"}]`,
	)
	// newest first, as GitLab lists them: v3 replaced v2's commits, v2
	// built on v1, and v1 predates the period
	f.Script("glab api --paginate projects/:fullpath/merge_requests/3/versions?per_page=100", `[
	  {"head_commit_sha":"v3","created_at":"2026-10-14T11:00:00Z"},
	  {"head_commit_sha":"v2","created_at":"2026-10-14T09:00:00Z"},
	  {"head_commit_sha":"v1","created_at":"2026-10-13T09:00:00Z"}]`)
	f.Script("git merge-base --is-ancestor v1 v2", "")
	f.Fail("git merge-base --is-ancestor v2 v3", shell.Result{Code: 1})
	f.Script("glab api --paginate projects/:fullpath/merge_requests/7/notes?per_page=100", `[
	  {"body":"approved this merge request","system":true,"created_at":"2026-10-14T09:00:00Z","author":{"username":"me"}},
	  {"body":"requested review from @me","system":true,"created_at":"2026-10-14T08:00:00Z","author":{"username":"bob"}},
	  {"body":"looks good","system":false,"created_at":"2026-10-14T10:00:00Z","author":{"username":"me"}}]`)

	got, err := hostops.NewHost("glab", ".").Pulls(t.Context(), "me", testkit.StandupSince)
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

// commitFacts reads numstat, the generated marks and base merges once
// for all commits, and keys each to its commit.
// A merge on a stacked branch checks the recorded base before main.
func TestCommitCI(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh api repos/{owner}/{repo}/commits/p1/check-runs?per_page=100", `{"check_runs":[
	  {"conclusion":"success","completed_at":"2026-10-14T09:00:00Z"},
	  {"conclusion":"failure","completed_at":"2026-10-14T10:00:00Z"},
	  {"conclusion":null,"completed_at":null}]}`)
	f.Script("gh api repos/{owner}/{repo}/commits/p1/status", `{"statuses":[]}`)
	f.Script("gh api repos/{owner}/{repo}/commits/p2/check-runs?per_page=100", `{"check_runs":[]}`)
	f.Script("gh api repos/{owner}/{repo}/commits/p2/status", `{"statuses":[]}`)
	f.Script("glab api projects/:fullpath/repository/commits/p3",
		`{"last_pipeline":{"status":"failed","updated_at":"2026-10-14T11:00:00Z"}}`)

	gh := hostops.NewHost("gh", ".")

	ci, err := gh.CommitCI(t.Context(), "p1")
	if err != nil || ci == nil || !ci.Red || !ci.At.Equal(testkit.StandupSince.Add(10*time.Hour)) {
		t.Errorf("p1 = %+v, %v; want red at 10:00", ci, err)
	}

	if ci, err := gh.CommitCI(t.Context(), "p2"); err != nil || ci != nil {
		t.Errorf("p2 = %+v, %v; want unknown with no checks", ci, err)
	}

	if ci, err := hostops.NewHost("glab", ".").CommitCI(t.Context(), "p3"); err != nil || ci == nil || !ci.Red {
		t.Errorf("p3 = %+v, %v; want a red pipeline", ci, err)
	}
}
