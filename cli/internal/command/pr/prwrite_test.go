package pr_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	prcmd "github.com/amberpixels/f10/cli/internal/command/pr"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

const (
	issueComments = "gh api --paginate repos/{owner}/{repo}/issues/4/comments?per_page=100"
	postTopLevel  = "gh api -X POST repos/{owner}/{repo}/issues/4/comments -f body=done"
)

func headPR() forge.PR {
	return forge.PR{Number: 4, Status: forge.Open, URL: "https://gh/o/r/pull/4", HeadSHA: "abcdef123"}
}

func TestCommentOncePosts(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh api user", `{"login":"me"}`)
	f.Script(issueComments, `[{"body":"done","html_url":"u1","user":{"login":"someone"}}]`)
	f.Script(postTopLevel, `{"id":9,"html_url":"https://gh/c9"}`)

	got, err := prcmd.CommentOnce(
		t.Context(),
		&bytes.Buffer{},
		hostops.NewHost("gh", t.TempDir()),
		headPR(),
		"",
		"",
		"done",
	)
	if err != nil || got != "https://gh/c9" {
		t.Errorf("commentOnce = %q, %v; want the new comment, another's same text not counting", got, err)
	}
}

// A retry finds its own earlier post and does not post again.
func TestCommentOnceFindsItsOwnPost(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh api user", `{"login":"Me"}`)
	f.Script(issueComments, `[{"body":"done\n","html_url":"u1","user":{"login":"me"}}]`)

	var warn bytes.Buffer

	got, err := prcmd.CommentOnce(t.Context(), &warn, hostops.NewHost("gh", t.TempDir()), headPR(), "", "", "done")
	if err != nil || got != "u1" || !strings.Contains(warn.String(), "already posted") {
		t.Errorf("commentOnce = %q, %v, %q; want the earlier post", got, err, warn.String())
	}

	if f.Called(postTopLevel) {
		t.Error("posted a second time")
	}
}

// A url naming no thread falls back to a top-level comment, and says so.
func TestCommentOnceFallsBack(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh api user", `{"login":"me"}`)
	f.Script(issueComments, `[]`)
	f.Script(postTopLevel, `{"id":9,"html_url":"https://gh/c9"}`)

	var warn bytes.Buffer

	got, err := prcmd.CommentOnce(t.Context(), &warn, hostops.NewHost("gh", t.TempDir()), headPR(),
		"https://gh/o/r/pull/4#issuecomment-5", "", "done")
	if err != nil || got != "https://gh/c9" || !strings.Contains(warn.String(), "names no thread") {
		t.Errorf("commentOnce = %q, %v, %q", got, err, warn.String())
	}
}

// A fallen-back reply carries its quote, since no thread says what it answers.
func TestCommentOnceQuotesAFallback(t *testing.T) {
	quoted := "gh api -X POST repos/{owner}/{repo}/issues/4/comments -f body=> The title\n\ndone"

	f := testkit.NewFakes(t)
	f.Script("gh api user", `{"login":"me"}`)
	f.Script(issueComments, `[]`)
	f.Script(quoted, `{"id":9,"html_url":"https://gh/c9"}`)

	if _, err := prcmd.CommentOnce(t.Context(), &bytes.Buffer{}, hostops.NewHost("gh", t.TempDir()), headPR(),
		"https://gh/o/r/pull/4#issuecomment-5", "The title", "done"); err != nil || !f.Called(quoted) {
		t.Errorf("commentOnce = %v; want the quote above the reply", err)
	}
}

const ghStatuses = "gh api --paginate repos/{owner}/{repo}/commits/abcdef123/statuses?per_page=100"

func TestStatusOnce(t *testing.T) {
	s := forge.CommitStatus{Name: "judge", State: "success", Description: "proceed"}
	set := "gh api -X POST repos/{owner}/{repo}/statuses/abcdef123 -f state=success -f context=judge -f description=proceed"

	f := testkit.NewFakes(t)
	f.Script(ghStatuses, `[{"context":"judge","state":"failure","description":"rethink"}]`)
	f.Script(set, `{}`)
	f.Fail("git rev-parse --verify 0123456^{commit}", shell.Result{Code: 128})

	h := hostops.NewHost("gh", t.TempDir())

	if line, err := prcmd.StatusOnce(t.Context(), &bytes.Buffer{}, h, headPR(), "abcdef1", s); err != nil ||
		line != "abcdef1 judge success" || !f.Called(set) {
		t.Errorf("statusOnce = %q, %v; want it set on the head", line, err)
	}

	if _, err := prcmd.StatusOnce(t.Context(), &bytes.Buffer{}, h, headPR(), "0123456", s); err == nil ||
		!strings.Contains(err.Error(), "head is abcdef1, not 0123456") {
		t.Errorf("a moved head: err = %v, want a refusal naming both", err)
	}
}

func TestStatusOnceLeavesTheSameAlone(t *testing.T) {
	s := forge.CommitStatus{Name: "judge", State: "success", Description: "proceed"}

	f := testkit.NewFakes(t)
	f.Script(ghStatuses, `[{"context":"judge","state":"success","description":"proceed"}]`)

	var warn bytes.Buffer

	if _, err := prcmd.StatusOnce(
		t.Context(),
		&warn,
		hostops.NewHost("gh", t.TempDir()),
		headPR(),
		"",
		s,
	); err != nil ||
		!strings.Contains(warn.String(), "already set") ||
		f.Called("gh api -X POST") {
		t.Errorf("statusOnce = %v, %q; want no second post", err, warn.String())
	}
}

// A push the host has not reflected yet is built on the head it reports, and
// takes the status; a commit off another line does not.
func TestStatusOnceTakesAPushAheadOfTheHead(t *testing.T) {
	dir := testkit.PrRepo(t)
	head := strings.TrimSpace(testkit.Git(t, dir, "rev-parse", "HEAD"))
	testkit.CommitFile(t, dir, "fix.txt")
	pushed := strings.TrimSpace(testkit.Git(t, dir, "rev-parse", "HEAD"))

	var posted string

	seam.Swap(t, &shell.Capture, func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		key := name + " " + strings.Join(args, " ")
		if strings.Contains(key, "-X POST") {
			posted = key
		}

		return shell.Result{Stdout: "[]"}, nil
	})

	p := forge.PR{Number: 4, Status: forge.Open, URL: "u", HeadSHA: head}
	s := forge.CommitStatus{Name: "judge", State: "success", Description: "ok"}

	line, err := prcmd.StatusOnce(t.Context(), &bytes.Buffer{}, hostops.NewHost("gh", dir), p, pushed[:7], s)
	if err != nil || !strings.HasPrefix(line, pushed[:7]) || !strings.Contains(posted, "statuses/"+pushed) {
		t.Errorf("statusOnce = %q, %v, posted %q; want the pushed commit", line, err, posted)
	}

	if _, err := prcmd.StatusOnce(
		t.Context(),
		&bytes.Buffer{},
		hostops.NewHost("gh", dir),
		forge.PR{HeadSHA: pushed},
		head[:7],
		s,
	); err == nil {
		t.Error("an older commit than the head took the status")
	}
}

func TestMineLine(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script("gh api user", `{"login":"Me"}`, `{"login":"other"}`)

	h, p := hostops.NewHost("gh", t.TempDir()), forge.PR{Author: "me"}

	for _, want := range []string{"mine: yes\n", "mine: no\n"} {
		if got := prcmd.MineLine(t.Context(), &bytes.Buffer{}, h, p); got != want {
			t.Errorf("mineLine = %q, want %q", got, want)
		}
	}
}

func TestMineLineUnknown(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Fail("gh api user", shell.Result{Code: 1, Stderr: "auth"})

	if got := prcmd.MineLine(
		t.Context(),
		&bytes.Buffer{},
		hostops.NewHost("gh", t.TempDir()),
		forge.PR{},
	); got != "mine: unknown\n" {
		t.Errorf("mineLine = %q; a failed login read is unknown, never no", got)
	}
}

func TestRequireOpen(t *testing.T) {
	if err := prcmd.RequireOpen(
		forge.PR{Status: forge.Merged, URL: "u"},
	); err == nil ||
		err.Error() != "u is merged: nothing written" {
		t.Errorf("merged: err = %v", err)
	}

	if err := prcmd.RequireOpen(forge.PR{Status: forge.Open}); err != nil {
		t.Errorf("glab's opened: err = %v", err)
	}
}

// pr create and pr edit name a bundle's riders after the lead id, whatever
// the draft said; a task alone, no task and a body-only edit keep the title.
func TestPullTitle(t *testing.T) {
	bundle := &ref.Ref{ID: "GH-1", Number: "1", Bundle: []string{"GH-1", "GH-2", "GH-3"}}

	for _, tc := range []struct {
		title string
		task  *ref.Ref
		want  string
	}{
		{"GH-1: Lead", bundle, "GH-1 (+GH-2, GH-3): Lead"},
		{"GH-1: Lead", &ref.Ref{ID: "GH-1", Number: "1"}, "GH-1: Lead"},
		{"GH-1: Lead", nil, "GH-1: Lead"},
		{"", bundle, ""},
	} {
		if got := prcmd.PullTitle(tc.title, tc.task); got != tc.want {
			t.Errorf("PullTitle(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}
