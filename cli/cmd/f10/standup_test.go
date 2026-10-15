package main

import (
	"bytes"
	"slices"
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

	if foreign := got[1]; foreign.Number != 7 || len(foreign.Reviews) != 2 {
		t.Errorf("foreign = %+v, want the approval and the comment", foreign)
	}
}

func TestRenderStandup(t *testing.T) {
	merged := standupSince.Add(5 * time.Hour)
	r := standup.Report{
		Since:  standupSince,
		Period: standup.PeriodLastStandup,
		Days:   []string{"2026-10-09", "2026-10-12"},
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
