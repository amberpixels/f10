package forge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/shell"
)

func TestEditPullGLab(t *testing.T) {
	script(t, map[string]string{"glab mr update 4 --title T --description B": ""})

	if err := (Host{Name: "glab"}).EditPull(t.Context(), "4", "T", "B"); err != nil {
		t.Fatal(err)
	}
}

// gh takes the body as a file, which is gone once the edit ran.
func TestEditPullGitHubBodyFile(t *testing.T) {
	var file string

	prev := shell.Capture
	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		if got := name + " " + strings.Join(args[:3], " "); got != "gh pr edit 4" || args[3] != "--body-file" {
			t.Fatalf("call = %s %v", name, args)
		}

		file = args[4]

		if b, err := os.ReadFile(file); err != nil || string(b) != "the body" {
			t.Errorf("body file = %q, %v", b, err)
		}

		return shell.Result{}, nil
	}

	t.Cleanup(func() { shell.Capture = prev })

	if err := (Host{Name: "gh"}).EditPull(t.Context(), "4", "", "the body"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("body file %s left behind", file)
	}
}

func TestEditPullNeedsSomething(t *testing.T) {
	if err := (Host{Name: "gh"}).EditPull(t.Context(), "4", "", ""); err == nil {
		t.Error("an edit with neither title nor body ran")
	}
}

// A reply attaches to the thread's top-level comment, and an inline review's
// url names the thread of its first comment.
func TestThreadGitHub(t *testing.T) {
	script(t, map[string]string{
		"gh api repos/{owner}/{repo}/pulls/comments/22":                                   `{"id":22,"in_reply_to_id":20}`,
		"gh api --paginate repos/{owner}/{repo}/pulls/4/reviews/9/comments?per_page=100":  `[{"id":30},{"id":31}]`,
		"gh api --paginate repos/{owner}/{repo}/pulls/4/reviews/10/comments?per_page=100": `[]`,
	})

	h := Host{Name: "gh"}

	for url, want := range map[string]string{
		"https://github.com/o/r/pull/4#discussion_r22":       "20",
		"https://github.com/o/r/pull/4#pullrequestreview-9":  "30",
		"https://github.com/o/r/pull/4#pullrequestreview-10": "",
		"https://github.com/o/r/pull/4#issuecomment-5":       "",
	} {
		got, err := h.Thread(t.Context(), 4, url)
		if want == "" {
			if !errors.Is(err, ErrNoThread) {
				t.Errorf("Thread(%s) = %q, %v; want ErrNoThread", url, got, err)
			}

			continue
		}

		if err != nil || got != want {
			t.Errorf("Thread(%s) = %q, %v; want %s", url, got, err, want)
		}
	}
}

func TestThreadGLab(t *testing.T) {
	script(t, map[string]string{
		"glab api --paginate projects/:fullpath/merge_requests/4/discussions?per_page=100": `[` +
			`{"id":"d1","individual_note":true,"notes":[{"id":1}]},` +
			`{"id":"d2","individual_note":false,"notes":[{"id":2},{"id":3}]}]`,
	})

	h := Host{Name: "glab"}

	if got, err := h.Thread(t.Context(), 4, "https://gl/x/-/merge_requests/4#note_3"); err != nil || got != "d2" {
		t.Errorf("thread note = %q, %v; want d2", got, err)
	}

	if _, err := h.Thread(t.Context(), 4, "https://gl/x/-/merge_requests/4#note_1"); !errors.Is(err, ErrNoThread) {
		t.Errorf("individual note: err = %v, want ErrNoThread", err)
	}
}

func TestThreadCommentsGitHubKeepsTheThread(t *testing.T) {
	script(t, map[string]string{
		"gh api --paginate repos/{owner}/{repo}/pulls/4/comments?per_page=100": `[` +
			`{"id":20,"body":"root","html_url":"u20","user":{"login":"bot"}},` +
			`{"id":21,"in_reply_to_id":20,"body":"reply","html_url":"u21","user":{"login":"me"}},` +
			`{"id":40,"body":"other","html_url":"u40","user":{"login":"me"}}]`,
	})

	got, err := (Host{Name: "gh"}).ThreadComments(t.Context(), 4, "", "20")
	if err != nil || len(got) != 2 || got[1].URL != "u21" || got[1].Author != "me" {
		t.Errorf("ThreadComments = %+v, %v", got, err)
	}
}

func TestPostCommentURLs(t *testing.T) {
	script(t, map[string]string{
		"gh api -X POST repos/{owner}/{repo}/pulls/4/comments/20/replies -f body=hi": `{"id":5,"html_url":"https://gh/r5"}`,
		"glab api -X POST projects/:fullpath/merge_requests/4/notes -f body=hi":      `{"id":77}`,
	})

	if got, err := (Host{Name: "gh"}).PostComment(
		t.Context(),
		4,
		"",
		"20",
		"hi",
	); err != nil ||
		got != "https://gh/r5" {
		t.Errorf("gh reply = %q, %v", got, err)
	}

	got, err := (Host{Name: "glab"}).PostComment(t.Context(), 4, "https://gl/x/-/merge_requests/4", "", "hi")
	if err != nil || got != "https://gl/x/-/merge_requests/4#note_77" {
		t.Errorf("glab note = %q, %v", got, err)
	}
}

func TestLatestStatus(t *testing.T) {
	script(t, map[string]string{
		"gh api --paginate repos/{owner}/{repo}/commits/abc/statuses?per_page=100": `[` +
			`{"context":"judge","state":"failure","description":"new"},` +
			`{"context":"judge","state":"success","description":"old"}]`,
		"glab api --paginate projects/:fullpath/repository/commits/abc/statuses?all=true&per_page=100&name=review+%28local%29": `[` +
			`{"id":1,"name":"review (local)","status":"success","description":"old"},` +
			`{"id":2,"name":"review (local)","status":"failed","description":"new"}]`,
	})

	got, err := (Host{Name: "gh"}).LatestStatus(t.Context(), "abc", "judge")
	if err != nil || got == nil || got.State != "failure" || got.Description != "new" {
		t.Errorf("gh = %+v, %v; want the newest", got, err)
	}

	got, err = (Host{Name: "glab"}).LatestStatus(t.Context(), "abc", "review (local)")
	if err != nil || got == nil || got.State != "failure" || got.Description != "new" {
		t.Errorf("glab = %+v, %v; want the newest, failed read as failure", got, err)
	}

	if got, err := (Host{Name: "gh"}).LatestStatus(t.Context(), "abc", "none"); err != nil || got != nil {
		t.Errorf("no such status = %+v, %v", got, err)
	}
}

func TestSetStatusGLabSpellsFailed(t *testing.T) {
	script(t, map[string]string{
		"glab api -X POST projects/:fullpath/statuses/abc -f state=failed -f name=judge -f description=d": "{}",
	})

	err := (Host{Name: "glab"}).SetStatus(
		t.Context(),
		"abc",
		CommitStatus{Name: "judge", State: "failure", Description: "d"},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreatePullSpellings(t *testing.T) {
	n := NewPull{Head: "feat", Base: "main", Title: "T", Body: "B", Assignees: []string{"@me"}}

	script(t, map[string]string{
		"gh pr create --head feat --base main --title T --body B --assignee @me": "",
		"glab mr create --source-branch feat --target-branch main --title T --description B --no-editor --yes " +
			"--assignee @me": "",
	})

	for _, name := range []string{"gh", "glab"} {
		if err := (Host{Name: name}).CreatePull(t.Context(), n); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
