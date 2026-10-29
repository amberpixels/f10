package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
)

const (
	issueComments = "gh api --paginate repos/{owner}/{repo}/issues/4/comments?per_page=100"
	postTopLevel  = "gh api -X POST repos/{owner}/{repo}/issues/4/comments -f body=done"
)

func headPR() pull {
	return pull{Number: 4, State: "OPEN", URL: "https://gh/o/r/pull/4", HeadRefOID: "abcdef123"}
}

func TestCommentOncePosts(t *testing.T) {
	f := newFakes(t)
	f.script("gh api user", `{"login":"me"}`)
	f.script(issueComments, `[{"body":"done","html_url":"u1","user":{"login":"someone"}}]`)
	f.script(postTopLevel, `{"id":9,"html_url":"https://gh/c9"}`)

	got, err := commentOnce(t.Context(), &bytes.Buffer{}, newHost("gh", t.TempDir()), headPR(), "", "done")
	if err != nil || got != "https://gh/c9" {
		t.Errorf("commentOnce = %q, %v; want the new comment, another's same text not counting", got, err)
	}
}

// A retry finds its own earlier post and does not post again.
func TestCommentOnceFindsItsOwnPost(t *testing.T) {
	f := newFakes(t)
	f.script("gh api user", `{"login":"Me"}`)
	f.script(issueComments, `[{"body":"done\n","html_url":"u1","user":{"login":"me"}}]`)

	var warn bytes.Buffer

	got, err := commentOnce(t.Context(), &warn, newHost("gh", t.TempDir()), headPR(), "", "done")
	if err != nil || got != "u1" || !strings.Contains(warn.String(), "already posted") {
		t.Errorf("commentOnce = %q, %v, %q; want the earlier post", got, err, warn.String())
	}

	if f.called(postTopLevel) {
		t.Error("posted a second time")
	}
}

// A url naming no thread falls back to a top-level comment, and says so.
func TestCommentOnceFallsBack(t *testing.T) {
	f := newFakes(t)
	f.script("gh api user", `{"login":"me"}`)
	f.script(issueComments, `[]`)
	f.script(postTopLevel, `{"id":9,"html_url":"https://gh/c9"}`)

	var warn bytes.Buffer

	got, err := commentOnce(t.Context(), &warn, newHost("gh", t.TempDir()), headPR(),
		"https://gh/o/r/pull/4#issuecomment-5", "done")
	if err != nil || got != "https://gh/c9" || !strings.Contains(warn.String(), "names no thread") {
		t.Errorf("commentOnce = %q, %v, %q", got, err, warn.String())
	}
}

const ghStatuses = "gh api --paginate repos/{owner}/{repo}/commits/abcdef123/statuses?per_page=100"

func TestStatusOnce(t *testing.T) {
	s := forge.CommitStatus{Name: "judge", State: "success", Description: "proceed"}
	set := "gh api -X POST repos/{owner}/{repo}/statuses/abcdef123 -f state=success -f context=judge -f description=proceed"

	f := newFakes(t)
	f.script(ghStatuses, `[{"context":"judge","state":"failure","description":"rethink"}]`)
	f.script(set, `{}`)

	h := newHost("gh", t.TempDir())

	if line, err := statusOnce(t.Context(), &bytes.Buffer{}, h, headPR(), "abcdef1", s); err != nil ||
		line != "abcdef1 judge success" || !f.called(set) {
		t.Errorf("statusOnce = %q, %v; want it set on the head", line, err)
	}

	if _, err := statusOnce(t.Context(), &bytes.Buffer{}, h, headPR(), "0123456", s); err == nil ||
		!strings.Contains(err.Error(), "head is abcdef1, not 0123456") {
		t.Errorf("a moved head: err = %v, want a refusal naming both", err)
	}
}

func TestStatusOnceLeavesTheSameAlone(t *testing.T) {
	s := forge.CommitStatus{Name: "judge", State: "success", Description: "proceed"}

	f := newFakes(t)
	f.script(ghStatuses, `[{"context":"judge","state":"success","description":"proceed"}]`)

	var warn bytes.Buffer

	if _, err := statusOnce(t.Context(), &warn, newHost("gh", t.TempDir()), headPR(), "", s); err != nil ||
		!strings.Contains(warn.String(), "already set") || f.called("gh api -X POST") {
		t.Errorf("statusOnce = %v, %q; want no second post", err, warn.String())
	}
}

// Writes need an open request: with no host there is none, and the branch
// is named.
func TestPRWritesNeedAnOpenPR(t *testing.T) {
	dir := prRepo(t)

	if _, _, err := runPR(t, dir, "edit", "--title", "x"); err == nil || err.Error() != "no open PR for main" {
		t.Errorf("edit: err = %v", err)
	}

	if _, _, err := runPR(t, dir, "edit"); err == nil || !strings.Contains(err.Error(), "--title, --body-file") {
		t.Errorf("edit with nothing: err = %v", err)
	}

	if _, _, err := runPR(t, dir, "status", "--name", "judge", "--state", "green", "-m", "x"); err == nil ||
		!strings.Contains(err.Error(), "success or failure") {
		t.Errorf("status with a bad state: err = %v", err)
	}
}
