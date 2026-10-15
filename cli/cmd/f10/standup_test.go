package main

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// The standup collectors are exercised against a scripted git and gh or
// glab: what each asks and what it reads back. Grouping is
// internal/standup's and tested there.

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

	for _, bad := range []string{"2d", "yesterday", ""} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) took it; the period's forms are not --since's", bad)
		}
	}
}

func TestRefBranch(t *testing.T) {
	remotes := []string{"origin", "up"}

	for in, want := range map[string]string{
		"FT-1/x":                "FT-1/x",
		"origin/FT-1/x":         "FT-1/x",
		"refs/remotes/up/a":     "a",
		"refs/heads/feature/ab": "feature/ab",
		"origin/HEAD":           "main",
		"other/x":               "other/x", // not a remote: a branch with a slash
	} {
		if got := refBranch(in, remotes, "main"); got != want {
			t.Errorf("refBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

// Only the user's email counts, matched exactly and in any case: a commit
// by someone whose name contains the user's is not theirs. A commit the
// default branch reaches is on it whatever ref --source named.
func TestUserCommits(t *testing.T) {
	f := newFakes(t)

	rec := func(fields ...string) string { return strings.Join(fields, "\x1f") }
	f.script("git log --branches --remotes --source "+sinceArg+" "+logFormat, strings.Join([]string{
		rec("a1", "Me@X.io", "2026-10-14T10:00:00+03:00", "p1", "origin/FT-2/b", "work on b"),
		rec("a2", "me@x.io", "2026-10-14T11:00:00+03:00", "p2 p3", "FT-2/b", "Merge main: into b"),
		rec("m1", "me@x.io", "2026-10-14T09:00:00+03:00", "p0", "FT-2/b", "release: v1"),
		rec("z1", "notme@x.io", "2026-10-14T12:00:00+03:00", "p4", "FT-2/b", "by someone named me"),
	}, "\n"))
	f.script("git log --format=%H "+sinceArg+" origin/main", "m1")

	got, err := userCommits(t.Context(), ".", "me@x.io", standupSince, "origin/main", "main", []string{"origin"})
	if err != nil {
		t.Fatal(err)
	}

	want := []standup.Commit{
		{
			SHA:     "a1",
			Subject: "work on b",
			At:      time.Date(2026, 10, 14, 7, 0, 0, 0, time.UTC),
			Branch:  "FT-2/b",
			Parents: []string{"p1"},
		},
		{
			SHA:     "a2",
			Subject: "Merge main: into b",
			At:      time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC),
			Branch:  "FT-2/b",
			Parents: []string{"p2", "p3"},
		},
		{
			SHA:     "m1",
			Subject: "release: v1",
			At:      time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC),
			Branch:  "main",
			Parents: []string{"p0"},
		},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d commits, want %d: %+v", len(got), len(want), got)
	}

	for i := range want {
		g, w := got[i], want[i]
		if g.SHA != w.SHA || g.Subject != w.Subject || !g.At.Equal(w.At) || g.Branch != w.Branch ||
			!slices.Equal(g.Parents, w.Parents) {
			t.Errorf("commit %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestRecordedBases(t *testing.T) {
	f := newFakes(t)
	f.script(cfgDeps, "branch.FT-2/b.f10-after-branch FT-1/a\nbranch.FT-3.f10-after-branch FT-2/b")

	got := recordedBases(t.Context(), ".")
	if got["FT-2/b"] != "FT-1/a" || got["FT-3"] != "FT-2/b" || len(got) != 2 {
		t.Errorf("bases = %v", got)
	}
}

const numstatShow = "git show --no-renames --numstat " + numstatFormat

func ghForcePushes(number int) string {
	return "gh api graphql -f query=" + forcePushesQuery + " -F owner={owner} -F name={repo} -F number=" + strconv.Itoa(
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

	f.script(ghSearch("author:me updated:>="+ghStamp, ghOwnFields), `[
	  {"number":5,"title":"FT-2: b","state":"OPEN","isDraft":true,"headRefName":"FT-2/b","baseRefName":"FT-1/a",
	   "url":"u5","author":{"login":"me"},"createdAt":"2026-10-14T08:00:00Z","changedFiles":0,"mergeCommit":null}]`)
	f.script(
		"gh api --paginate repos/{owner}/{repo}/pulls/5/commits?per_page=100",
		`[{"sha":"a1","commit":{"message":"work on b\n\nbody","author":{"date":"2026-10-14T07:00:00Z"}}},{"sha":"a2"}]`,
	)
	f.script(ghForcePushes(5), `{"data":{"repository":{"pullRequest":{"timelineItems":{"nodes":[
	  {"createdAt":"2026-10-14T09:00:00Z","actor":{"login":"me"},"beforeCommit":{"oid":"b0"},"afterCommit":{"oid":"b1"}},
	  {"createdAt":"2026-10-14T10:00:00Z","actor":{"login":"bob"},"beforeCommit":{"oid":"b1"},"afterCommit":{"oid":"b2"}}]}}}}}`)
	f.script(ghSearch("reviewed-by:me updated:>="+ghStamp, ghForeignFields+",reviews"), `[
	  {"number":9,"title":"theirs","state":"MERGED","url":"u9","author":{"login":"bob"},
	   "reviews":[{"author":{"login":"me"},"submittedAt":"2026-10-14T09:00:00Z"}]},
	  {"number":5,"title":"FT-2: b","state":"OPEN","url":"u5","author":{"login":"me"},"reviews":[]}]`)
	f.script(ghSearch("is:merged merged:>="+ghStamp, ghForeignFields), `[
	  {"number":9,"title":"theirs","state":"MERGED","url":"u9","author":{"login":"bob"},
	   "mergedAt":"2026-10-14T10:00:00Z","mergedBy":{"login":"me"}}]`)

	got, err := host{name: "gh", dir: "."}.standupPulls(t.Context(), "me", standupSince)
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

	got, err := host{name: "glab", dir: "."}.standupPulls(t.Context(), "me", standupSince)
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
	if err := renderStandup(&out, r); err != nil {
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
func scriptStandupGit(f *fakes, email, originHead string) {
	f.script("git config user.email", email)
	f.script("git remote", "origin")
	f.script(headRef, originHead)
	f.script("git log --branches --remotes --source "+sinceArg+" "+logFormat,
		strings.Join([]string{"a1", "me@x.io", "2026-10-14T10:00:00Z", "p1", "FT-2/b", "work"}, "\x1f"))
	f.script("git log --format=%H "+sinceArg+" origin/main", "")
	f.script(numstatShow+" a1", "")
	f.script(cfgDeps, "")
}

func TestCollectStandupWithoutHostReportsCommitsOnly(t *testing.T) {
	f := newFakes(t)
	scriptStandupGit(f, "me@x.io", "origin/main")

	in, err := collectStandup(t.Context(), standupSource{dir: ".", main: ".", prefix: "FT", noHost: true}, standupSince)
	if err != nil {
		t.Fatal(err)
	}

	if in.Host != "" || in.User.Login != "" || len(in.PRs) != 0 || len(in.Commits) != 1 || in.DefaultBranch != "main" {
		t.Errorf("input = %+v, want one commit, no host, default main", in)
	}
}

// A host CLI that fails is the command's error, never commits only.
func TestCollectStandupFailsOnAFailingHost(t *testing.T) {
	f := newFakes(t)
	scriptStandupGit(f, "me@x.io", "origin/main")
	f.answers["gh api user"] = []shell.Result{{Code: 1, Stderr: "not logged in"}}

	_, err := collectStandup(
		t.Context(),
		standupSource{dir: ".", main: ".", host: host{name: "gh", dir: "."}},
		standupSince,
	)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("err = %v, want gh's own error", err)
	}
}

func TestCollectStandupNeedsAGitEmail(t *testing.T) {
	f := newFakes(t)
	f.script("git config user.email", "")

	_, err := collectStandup(t.Context(), standupSource{dir: ".", noHost: true}, standupSince)
	if err == nil || !strings.Contains(err.Error(), "user.email") {
		t.Errorf("err = %v, want the user.email hint", err)
	}
}

// Without origin/HEAD the host names the default branch; without a host
// either, standup refuses rather than read landed commits as unfinished.
func TestStandupDefaultFallsBackToTheHost(t *testing.T) {
	f := newFakes(t)
	f.script(headRef, "")
	f.script("gh repo view --json defaultBranchRef", `{"defaultBranchRef":{"name":"trunk"}}`)
	f.script("git rev-parse --verify --quiet refs/remotes/origin/trunk", "abc")

	ref, name, err := standupDefault(
		t.Context(),
		standupSource{dir: ".", host: host{name: "gh", dir: "."}},
		[]string{"origin"},
	)
	if err != nil || ref != "refs/remotes/origin/trunk" || name != "trunk" {
		t.Errorf("default = %q, %q, %v, want origin's trunk", ref, name, err)
	}

	if _, _, err := standupDefault(
		t.Context(),
		standupSource{dir: ".", noHost: true},
		[]string{"origin"},
	); err == nil ||
		!strings.Contains(err.Error(), "set-head") {
		t.Errorf("err = %v, want the set-head hint", err)
	}
}

// Upkeep-only tasks leave the per-task lines for one line of their own.
func TestRenderStandupFoldsUpkeep(t *testing.T) {
	r := standup.Standup{Since: standupSince, Projects: []standup.Project{{Tasks: []standup.Task{
		{ID: "FT-1", State: standup.StateInReview, Class: standup.ClassUpkeep},
		{ID: "FT-2", State: standup.StateInProgress, Class: standup.ClassWork},
		{ID: "FT-3", State: standup.StateInReview, Class: standup.ClassUpkeep},
	}}}}

	var out bytes.Buffer
	if err := renderStandup(&out, r); err != nil {
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
func TestCommitFacts(t *testing.T) {
	f := newFakes(t)

	f.script(
		numstatShow+" c1 c2",
		"\x1ec1\n\n3\t1\tgo.sum\n10\t2\tapi/client.go\n-\t-\tlogo.png\n\x1ec2\n\n1\t1\tz_gen.go",
	)
	f.script("git check-attr linguist-generated -- api/client.go go.sum logo.png z_gen.go",
		"api/client.go: linguist-generated: unspecified\ngo.sum: linguist-generated: unspecified\n"+
			"logo.png: linguist-generated: set\nz_gen.go: linguist-generated: unspecified")
	f.script("git grep -l -E "+generatedHeader+" c1 c2 -- api/client.go z_gen.go", "c1:api/client.go")
	// m1 merged main's tip, m2 merged a feature branch main has since taken in
	f.script("git rev-list --first-parent --parents origin/main --not mainTip", "")
	f.script("git rev-parse --verify --quiet origin/main^{commit}", "mainTip")
	f.script("git rev-list --first-parent --parents origin/main --not feat", "t3 t2\nt2 t1\nt1 mergeOfFeat")

	commits := []standup.Commit{
		{SHA: "c1", Parents: []string{"p"}, Branch: "FT-1/a"},
		{SHA: "c2", Parents: []string{"p"}, Branch: "FT-1/a"},
		{SHA: "m1", Parents: []string{"c2", "mainTip"}, Branch: "FT-1/a"},
		{SHA: "m2", Parents: []string{"c2", "feat"}, Branch: "FT-1/a"},
	}
	commitFacts(t.Context(), ".", commits, "origin/main", "main", nil)

	c1 := commits[0].Files
	if len(c1) != 3 || c1[0].Path != "go.sum" || c1[0].Added != 3 || c1[1].Generated != true ||
		!c1[2].Binary || !c1[2].Generated {
		t.Errorf("c1 files = %+v", c1)
	}

	if !commits[2].BaseMerge || commits[3].BaseMerge {
		t.Errorf("base merges: m1 %v, m2 %v; want m1 only", commits[2].BaseMerge, commits[3].BaseMerge)
	}
}

// A merge on a stacked branch checks the recorded base before main.
func TestOnFirstParentLineOfARecordedBase(t *testing.T) {
	f := newFakes(t)
	f.script("git rev-parse --verify --quiet refs/remotes/origin/FT-1/a", "baseTip")
	f.script("git rev-list --first-parent --parents refs/remotes/origin/FT-1/a --not b2", "baseTip b2")

	commits := []standup.Commit{{SHA: "m", Parents: []string{"x", "b2"}, Branch: "FT-2/b"}}
	commitFacts(t.Context(), ".", commits, "origin/main", "main", map[string]string{"FT-2/b": "FT-1/a"})

	if !commits[0].BaseMerge {
		t.Error("a merge of the recorded base is a base merge")
	}

	if f.called("git rev-list --first-parent --parents origin/main") {
		t.Error("walked main after the recorded base already answered")
	}
}

func TestSamePatches(t *testing.T) {
	f := newFakes(t)
	f.script("git range-diff --no-color origin/main b0 b1",
		"1:  aaaaaaa = 1:  bbbbbbb FT-1: one\n2:  ccccccc = 2:  ddddddd FT-1: two")
	f.script("git range-diff --no-color origin/main b1 b2",
		"1:  aaaaaaa = 1:  bbbbbbb FT-1: one\n2:  ccccccc ! 2:  eeeeeee FT-1: two\n    @@ x.go\n    -a\n    +b")
	f.script("git range-diff --no-color origin/main b2 b3", "1:  aaaaaaa < -:  ------- FT-1: dropped")
	f.fail(
		"git range-diff --no-color origin/main gone b4",
		shell.Result{Code: 128, Stderr: "fatal: bad revision 'gone'"},
	)

	for _, tc := range []struct {
		before, after string
		want          string
	}{{"b0", "b1", "true"}, {"b1", "b2", "false"}, {"b2", "b3", "false"}, {"gone", "b4", "unknown"}, {"", "b5", "unknown"}} {
		got := "unknown"
		if p := samePatches(t.Context(), ".", "origin/main", tc.before, tc.after); p != nil {
			got = strconv.FormatBool(*p)
		}

		if got != tc.want {
			t.Errorf("%s..%s = %s, want %s", tc.before, tc.after, got, tc.want)
		}
	}
}

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

	gh := host{name: "gh", dir: "."}

	ci, err := gh.commitCI(t.Context(), "p1")
	if err != nil || ci == nil || !ci.Red || !ci.At.Equal(standupSince.Add(10*time.Hour)) {
		t.Errorf("p1 = %+v, %v; want red at 10:00", ci, err)
	}

	if ci, err := gh.commitCI(t.Context(), "p2"); err != nil || ci != nil {
		t.Errorf("p2 = %+v, %v; want unknown with no checks", ci, err)
	}

	if ci, err := (host{name: "glab", dir: "."}).commitCI(t.Context(), "p3"); err != nil || ci == nil || !ci.Red {
		t.Errorf("p3 = %+v, %v; want a red pipeline", ci, err)
	}
}

// A merge made on the default branch merges into it: unpushed, its second
// parent is origin's tip, and it must still not read as a base merge.
func TestCommitFactsMergeOnTheDefaultBranch(t *testing.T) {
	f := newFakes(t)
	f.fail("git merge-base --is-ancestor local origin/main", shell.Result{Code: 1})
	f.script("git merge-base --is-ancestor pushed origin/main", "")
	f.script("git rev-list --first-parent --parents origin/main --not remoteTip", "pushed mine remoteTip\nmine m0")

	commits := []standup.Commit{
		{SHA: "local", Parents: []string{"mine", "remoteTip"}, Branch: "main"},
		{SHA: "pushed", Parents: []string{"mine", "remoteTip"}, Branch: "main"},
	}
	commitFacts(t.Context(), ".", commits, "origin/main", "main", nil)

	if commits[0].BaseMerge || commits[1].BaseMerge {
		t.Errorf("base merge: unpushed %v, pushed %v; want neither", commits[0].BaseMerge, commits[1].BaseMerge)
	}
}

// A CI read that fails, as for a parent the host never saw, leaves the
// commit's CI unknown instead of failing the report; commits from before
// the period are never asked about.
func TestCollectStandupToleratesAnUnknownParent(t *testing.T) {
	f := newFakes(t)
	f.script("git config user.email", "me@x.io")
	f.script("git remote", "origin")
	f.script(headRef, "origin/main")
	f.script("git log --branches --remotes --source "+sinceArg+" "+logFormat,
		"t1\x1fme@x.io\x1f2026-10-14T10:00:00Z\x1fp1\x1fFT-2/b\x1ftest tweak\n"+
			"t0\x1fme@x.io\x1f2026-10-13T10:00:00Z\x1fp0\x1fFT-2/b\x1fold test tweak")
	f.script("git log --format=%H "+sinceArg+" origin/main", "")
	f.script(numstatShow+" t1 t0", "\x1et1\n\n1\t1\ta_test.go\n\x1et0\n\n1\t1\ta_test.go")
	f.script(cfgDeps, "")
	f.script("git check-attr linguist-generated -- a_test.go", "a_test.go: linguist-generated: unspecified")
	f.script("git grep -l -E "+generatedHeader+" t1 t0 -- a_test.go", "")
	f.script("gh api user", `{"login":"me"}`)
	f.script(ghSearch("author:me updated:>="+ghStamp, ghOwnFields), `[]`)
	f.script(ghSearch("reviewed-by:me updated:>="+ghStamp, ghForeignFields+",reviews"), `[]`)
	f.script(ghSearch("is:merged merged:>="+ghStamp, ghForeignFields), `[]`)
	f.fail("gh api repos/{owner}/{repo}/commits/p1/check-runs?per_page=100",
		shell.Result{Code: 1, Stderr: "No commit found for SHA: p1 (HTTP 422)"})

	in, err := collectStandup(
		t.Context(),
		standupSource{dir: ".", main: ".", prefix: "FT", host: host{name: "gh", dir: "."}},
		standupSince,
	)
	if err != nil {
		t.Fatalf("a parent the host never saw failed the report: %v", err)
	}

	if in.Commits[0].ParentCI != nil {
		t.Errorf("CI = %+v, want unknown", in.Commits[0].ParentCI)
	}

	if f.called("gh api repos/{owner}/{repo}/commits/p0/") {
		t.Error("asked CI for a commit from before the period")
	}
}

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
	if err := renderStandup(&out, r); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"== ACME (svc-a, svc-b)\n", "svc-a#12, svc-b#40 -> svc-b#41", "== r3\n", "R3-2       shipped      #5"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render missing %q:\n%s", want, out.String())
		}
	}
}
