package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/shell"
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

	got, err := commentOnce(t.Context(), &bytes.Buffer{}, newHost("gh", t.TempDir()), headPR(), "", "", "done")
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

	got, err := commentOnce(t.Context(), &warn, newHost("gh", t.TempDir()), headPR(), "", "", "done")
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
		"https://gh/o/r/pull/4#issuecomment-5", "", "done")
	if err != nil || got != "https://gh/c9" || !strings.Contains(warn.String(), "names no thread") {
		t.Errorf("commentOnce = %q, %v, %q", got, err, warn.String())
	}
}

// A fallen-back reply carries its quote, since no thread says what it answers.
func TestCommentOnceQuotesAFallback(t *testing.T) {
	quoted := "gh api -X POST repos/{owner}/{repo}/issues/4/comments -f body=> The title\n\ndone"

	f := newFakes(t)
	f.script("gh api user", `{"login":"me"}`)
	f.script(issueComments, `[]`)
	f.script(quoted, `{"id":9,"html_url":"https://gh/c9"}`)

	if _, err := commentOnce(t.Context(), &bytes.Buffer{}, newHost("gh", t.TempDir()), headPR(),
		"https://gh/o/r/pull/4#issuecomment-5", "The title", "done"); err != nil || !f.called(quoted) {
		t.Errorf("commentOnce = %v; want the quote above the reply", err)
	}
}

const ghStatuses = "gh api --paginate repos/{owner}/{repo}/commits/abcdef123/statuses?per_page=100"

func TestStatusOnce(t *testing.T) {
	s := forge.CommitStatus{Name: "judge", State: "success", Description: "proceed"}
	set := "gh api -X POST repos/{owner}/{repo}/statuses/abcdef123 -f state=success -f context=judge -f description=proceed"

	f := newFakes(t)
	f.script(ghStatuses, `[{"context":"judge","state":"failure","description":"rethink"}]`)
	f.script(set, `{}`)
	f.fail("git rev-parse --verify 0123456^{commit}", shell.Result{Code: 128})

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

// A push the host has not reflected yet is built on the head it reports, and
// takes the status; a commit off another line does not.
func TestStatusOnceTakesAPushAheadOfTheHead(t *testing.T) {
	dir := prRepo(t)
	head := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	commitFile(t, dir, "fix.txt")
	pushed := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	var posted string

	prev := shell.Capture
	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		key := name + " " + strings.Join(args, " ")
		if strings.Contains(key, "-X POST") {
			posted = key
		}

		return shell.Result{Stdout: "[]"}, nil
	}

	t.Cleanup(func() { shell.Capture = prev })

	p := pull{Number: 4, State: "OPEN", URL: "u", HeadRefOID: head}
	s := forge.CommitStatus{Name: "judge", State: "success", Description: "ok"}

	line, err := statusOnce(t.Context(), &bytes.Buffer{}, newHost("gh", dir), p, pushed[:7], s)
	if err != nil || !strings.HasPrefix(line, pushed[:7]) || !strings.Contains(posted, "statuses/"+pushed) {
		t.Errorf("statusOnce = %q, %v, posted %q; want the pushed commit", line, err, posted)
	}

	if _, err := statusOnce(
		t.Context(),
		&bytes.Buffer{},
		newHost("gh", dir),
		pull{HeadRefOID: pushed},
		head[:7],
		s,
	); err == nil {
		t.Error("an older commit than the head took the status")
	}
}

func TestMineLine(t *testing.T) {
	f := newFakes(t)
	f.script("gh api user", `{"login":"Me"}`, `{"login":"other"}`)

	h, p := newHost("gh", t.TempDir()), pull{Author: pullUser{Login: "me"}}

	for _, want := range []string{"mine: yes\n", "mine: no\n"} {
		if got := mineLine(t.Context(), &bytes.Buffer{}, h, p); got != want {
			t.Errorf("mineLine = %q, want %q", got, want)
		}
	}
}

func TestMineLineUnknown(t *testing.T) {
	f := newFakes(t)
	f.fail("gh api user", shell.Result{Code: 1, Stderr: "auth"})

	if got := mineLine(t.Context(), &bytes.Buffer{}, newHost("gh", t.TempDir()), pull{}); got != "mine: unknown\n" {
		t.Errorf("mineLine = %q; a failed login read is unknown, never no", got)
	}
}

func TestRequireOpen(t *testing.T) {
	if err := requireOpen(
		pull{State: "MERGED", URL: "u"},
	); err == nil ||
		err.Error() != "u is merged: nothing written" {
		t.Errorf("merged: err = %v", err)
	}

	if err := requireOpen(pull{State: "opened"}); err != nil {
		t.Errorf("glab's opened: err = %v", err)
	}
}

// create refuses before any host call when origin lacks the base or the
// branch at HEAD, naming the push that fixes it.
func TestPRCreateNeedsBothOnOrigin(t *testing.T) {
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("the body\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := prRepo(t)
	git(t, dir, "switch", "-q", "-c", "base")
	commitFile(t, dir, "base.txt")
	git(t, dir, "switch", "-q", "-c", "feat")
	git(t, dir, "config", "branch.feat."+cfgAfterBranch, "base")

	if _, _, err := runPR(t, dir, "create", "--title", "T", "--body-file", body); err == nil ||
		!strings.Contains(err.Error(), "base base is not on origin") {
		t.Errorf("unpushed base: err = %v", err)
	}

	git(t, dir, "push", "-q", "origin", "base")

	if _, _, err := runPR(t, dir, "create", "--title", "T", "--body-file", body); err == nil ||
		!strings.Contains(err.Error(), "git push -u origin feat") {
		t.Errorf("unpushed head: err = %v", err)
	}

	git(t, dir, "push", "-q", "origin", "feat")
	commitFile(t, dir, "feat.txt")

	if _, _, err := runPR(t, dir, "create", "--title", "T", "--body-file", body); err == nil ||
		!strings.Contains(err.Error(), "at HEAD") {
		t.Errorf("head ahead of origin: err = %v", err)
	}

	if _, _, err := runPR(t, dir, "create", "--body-file", body); err == nil ||
		!strings.Contains(err.Error(), "title") {
		t.Errorf("no title: err = %v", err)
	}
}

// The url comes from reading the new request back, not from create's output.
func TestCreatePullReadsTheURLBack(t *testing.T) {
	f := newFakes(t)
	f.script("gh pr create --head feat --base base --title T --body B --label x --draft", "noise\n")
	f.script("gh pr view feat --json state,url,number,title,body,baseRefName,mergeStateStatus,mergeCommit,author",
		`{"state":"OPEN","url":"https://gh/o/r/pull/9","number":9}`)

	got, err := newHost("gh", t.TempDir()).createPull(t.Context(),
		forge.NewPull{Head: "feat", Base: "base", Title: "T", Body: "B", Labels: []string{"x"}, Draft: true})
	if err != nil || got != "https://gh/o/r/pull/9" {
		t.Errorf("createPull = %q, %v", got, err)
	}
}
