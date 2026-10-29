package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// A fakes stands in for git, wt, herdr and the driver: every call recorded,
// every answer scripted, nothing executed. A key scripted with several
// answers hands them out in order, repeating the last, so a listing changes.
type fakes struct {
	// mu guards calls and answers: the board's collectors call concurrently
	mu      sync.Mutex
	t       *testing.T
	calls   []string
	answers map[string][]shell.Result
	has     map[string]bool
	// absent answers a call with a key under one of these prefixes, and no
	// script of its own, the way git answers a ref or config it lacks
	absent []string
}

func newFakes(t *testing.T) *fakes {
	t.Helper()

	f := &fakes{t: t, answers: map[string][]shell.Result{}, has: map[string]bool{"herdr": true}}
	// Herdr shows no workspace until a test says otherwise
	f.script("herdr workspace list", `{"result":{"workspaces":[]}}`)
	// no local branch carries a task id past a prefix until a test says so
	f.script(allHeads, "")
	// no branch is linked to a task, origin has no task branch, and finish
	// measures an empty branch (so records no size), until a test says so
	f.absent = []string{
		linkedKeys, riderKeys, "git for-each-ref --format=%(refname) refs/remotes/origin/", "git diff --numstat ",
		"git rev-list --ancestry-path --merges",
		// no base tip is recorded until a test says so
		"git merge-base refs/heads/",
	}

	prevExec, prevCapture, prevHas, prevProject := gitx.Exec, shell.Capture, shell.Has, projectOf

	// task refs need the project from git, which these fakes do not script
	projectOf = func(context.Context, string) string { return "" }

	gitx.Exec = func(_ context.Context, _ string, args ...string) (string, error) {
		res := f.answer("git " + strings.Join(args, " "))
		if res.Code != 0 {
			return "", &gitx.ExitError{
				Args: args, Code: res.Code, Stdout: strings.TrimSpace(res.Stdout), Stderr: res.Stderr,
			}
		}

		return strings.TrimSpace(res.Stdout), nil
	}

	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		return f.answer(filepath.Base(name) + " " + strings.Join(args, " ")), nil
	}

	shell.Has = func(name string) bool { return f.has[name] }

	t.Cleanup(func() { gitx.Exec, shell.Capture, shell.Has, projectOf = prevExec, prevCapture, prevHas, prevProject })
	t.Setenv("HERDR_ENV", "1")

	return f
}

func (f *fakes) answer(key string) shell.Result {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, key)

	queue, ok := f.answers[key]
	if !ok {
		for _, p := range f.absent {
			if strings.HasPrefix(key, p) {
				return shell.Result{}
			}
		}

		// no branch records riders until a test says so
		if strings.HasPrefix(key, "git config --get branch.") && strings.HasSuffix(key, "."+cfgRiders) {
			return shell.Result{}
		}

		// no branch is stacked on another, for finish's size, until a test says so
		if strings.HasPrefix(key, "git config --get branch.") && strings.HasSuffix(key, "."+cfgAfterBranch) {
			return shell.Result{}
		}

		// drive records its chain on every branch it opens
		if strings.HasPrefix(key, "git config branch.") && strings.Contains(key, "."+cfgChain+" ") {
			return shell.Result{}
		}

		// drive records its finish's progress on every branch it finishes
		if strings.HasPrefix(key, "git config ") && strings.Contains(key, "."+ref.FinishKey) {
			return shell.Result{}
		}

		// no branch records an agent profile, nor a model from before profiles,
		// until a test says so
		if strings.HasPrefix(key, "git config --get branch.") &&
			(strings.HasSuffix(key, "."+cfgAgent) || strings.HasSuffix(key, "."+cfgModel)) {
			return shell.Result{}
		}

		// start and drive record the flow on every branch they open, and
		// none records one until a test says so
		if strings.HasPrefix(key, "git config ") && strings.Contains(key, "."+cfgFlow) {
			return shell.Result{}
		}

		f.t.Fatalf("unscripted call: %q\nscripted:\n  %s", key, strings.Join(f.keys(), "\n  "))
	}

	if len(queue) > 1 {
		f.answers[key] = queue[1:]
	}

	return queue[0]
}

func (f *fakes) keys() []string {
	var ks []string
	for k := range f.answers {
		ks = append(ks, k)
	}

	return ks
}

// script sets the answers for key: one per call, the last repeating.
func (f *fakes) script(key string, stdouts ...string) {
	f.answers[key] = nil
	for _, s := range stdouts {
		f.answers[key] = append(f.answers[key], shell.Result{Stdout: s})
	}
}

func (f *fakes) fail(key string, res shell.Result) { f.answers[key] = []shell.Result{res} }

func (f *fakes) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}

	return false
}

// fakeDriver is an executable at <root>/driver so driver.Find accepts it;
// what it answers comes from the fakes, never from running it.
func fakeDriver(t *testing.T) *driver.Driver {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, "driver")

	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return driver.Find(root, root)
}

const (
	linkedKeys = "git config --get-regexp " + cfgTaskKeys
	riderKeys  = "git config --get-regexp ^branch\\..*\\." + cfgRiders + "$"
	refsOrigin = "git for-each-ref --format=%(refname) refs/remotes/origin/GH-1 refs/remotes/origin/GH-1-* refs/remotes/origin/GH-1+* refs/remotes/origin/GH-1+*/*"
	refsLocal  = "git for-each-ref --format=%(refname) refs/heads/GH-1 refs/heads/GH-1-* refs/heads/GH-1+* refs/heads/GH-1+*/*"
	refsAfter  = "git for-each-ref --format=%(refname) refs/heads/GH-7 refs/heads/GH-7-* refs/heads/GH-7+* refs/heads/GH-7+*/*"
	refsAfterO = "git for-each-ref --format=%(refname) refs/remotes/origin/GH-7 refs/remotes/origin/GH-7-* refs/remotes/origin/GH-7+* refs/remotes/origin/GH-7+*/*"
	allHeads   = "git for-each-ref --format=%(refname) refs/heads/"
	shaAfter   = "git rev-parse --verify --quiet GH-7/base"
	cfgTask    = "git config branch.GH-1.f10-after GH-7"
	cfgBranch  = "git config branch.GH-1.f10-after-branch GH-7/base"
	worktrees  = "git worktree list --porcelain"
	shaLocal   = "git rev-parse --verify --quiet refs/heads/release/1.2"
	shaRemote  = "git rev-parse --verify --quiet refs/remotes/release/1.2"
	shaOrigin  = "git rev-parse --verify --quiet refs/remotes/origin/release/1.2"
	headRef    = "git symbolic-ref --short refs/remotes/origin/HEAD"
	shaMain    = "git rev-parse --verify --quiet refs/remotes/origin/main"
	opened     = `{"result":{"workspace":{"workspace_id":"ws:3"},"root_pane":{"pane_id":"pane:7"},"already_open":false}}`
	reopened   = `{"result":{"workspace":{"workspace_id":"ws:3"},"root_pane":{"pane_id":"pane:7"},"already_open":true}}`
	agentFound = `{"result":{"agent":{"name":"claude-by-hand"}}}`
)

// agentOf is the name start gives the agent of a task with no suffix,
// checked out at the sibling path these tests use.
func agentOf(id string) string { return agentctl.AgentName(id, "", "/code/repo."+id) }

var gh1 = ref.Ref{ID: "GH-1", Number: "1", Origin: ref.OriginExplicit}

// missing is what rev-parse --verify --quiet answers for a ref that is not there.
var missing = shell.Result{Code: 1}

// refsKey is the for-each-ref call that looks for task id's branches under root.
func refsKey(root, id string) string {
	return "git for-each-ref --format=%(refname) " + strings.Join(taskRefs(root, id), " ")
}

// heads prefixes branch names the way for-each-ref prints them.
func heads(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("refs/heads/" + n + "\n")
	}

	return b.String()
}

// porcelain is a worktree listing of main plus the given branch/path pairs.
func porcelain(pairs ...string) string {
	var b strings.Builder

	b.WriteString("worktree /code/repo\nHEAD a\nbranch refs/heads/main\n")

	for i := 0; i+1 < len(pairs); i += 2 {
		b.WriteString("\nworktree " + pairs[i+1] + "\nHEAD b\nbranch refs/heads/" + pairs[i] + "\n")
	}

	return b.String()
}

// titled is a title lookup that answers with one title, or fails.
func titled(title string, err error) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return title, err }
}

func TestBranchName(t *testing.T) {
	cases := []struct {
		name     string
		driver   bool
		answer   shell.Result
		title    func(context.Context) (string, error)
		suffix   string
		want     string
		wantNote string
	}{
		{name: "no driver slugs the title", title: titled("Add dark mode", nil), want: "GH-1/add-dark-mode"},
		{
			name:   "driver names the branch",
			driver: true,
			answer: shell.Result{Stdout: "GH-1/short-slug\n"},
			title:  titled("unused", nil),
			want:   "GH-1/short-slug",
		},
		{
			name:   "driver declining slugs the title",
			driver: true,
			answer: shell.Result{Code: 3},
			title:  titled("Add dark mode", nil),
			want:   "GH-1/add-dark-mode",
		},
		{
			name:   "suffix follows the driver's name",
			driver: true,
			answer: shell.Result{Stdout: "GH-1/slug"},
			suffix: "v2",
			want:   "GH-1/slug-v2",
		},
		{
			name:   "suffix follows the slug",
			title:  titled("Add dark mode", nil),
			suffix: "v2",
			want:   "GH-1/add-dark-mode-v2",
		},
		{
			name:     "no title is the bare id, with the reason",
			title:    titled("", errors.New("gh: offline")),
			want:     "GH-1",
			wantNote: "no title for GH-1 (gh: offline), so the branch is the bare id",
		},
		{
			name:     "a title with no slug in it is the bare id",
			title:    titled("Исправить", nil),
			want:     "GH-1",
			wantNote: "the title of GH-1 yields no slug, so the branch is the bare id",
		},
		{
			name:     "no title source is the bare id",
			want:     "GH-1",
			wantNote: "no title source for GH-1, so the branch is the bare id",
		},
		{
			name:     "suffix follows the bare id",
			suffix:   "attempt2",
			want:     "GH-1-attempt2",
			wantNote: "no title source for GH-1, so the branch is the bare id",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakes(t)

			var d *driver.Driver
			if c.driver {
				d = fakeDriver(t)
				f.fail("driver branch 1", c.answer)
			}

			got, note, err := branchName(t.Context(), d, gh1, c.suffix, c.title)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.want {
				t.Errorf("branchName = %q, want %q", got, c.want)
			}

			if note != c.wantNote {
				t.Errorf("note = %q, want %q", note, c.wantNote)
			}
		})
	}
}

func TestBranchNameDriverFailure(t *testing.T) {
	f := newFakes(t)
	f.fail("driver branch 1", shell.Result{Code: 1, Stderr: "tracker down"})

	_, _, err := branchName(t.Context(), fakeDriver(t), gh1, "", titled("unused", nil))
	if err == nil || !strings.Contains(err.Error(), "tracker down") {
		t.Errorf("err = %v, want the driver's stderr", err)
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct{ title, want string }{
		{title: "Add dark mode", want: "add-dark-mode"},
		{title: "  f10 start: --after <task-id> records a dependency!  ", want: "f10-start-after-task-id-records-a"},
		{title: "Fix (again) the CSV/XLSX export", want: "fix-again-the-csv-xlsx-export"},
		{
			title: "Default task ids from the project name, default branches as <ID>/<slug>",
			want:  "default-task-ids-from-the-project-name",
		},
		{title: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{title: "exactly-forty-characters-long-slug-here!", want: "exactly-forty-characters-long-slug-here"},
		{title: "Поддержка кириллицы", want: ""},
		{title: "---", want: ""},
	}

	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			if got := slugify(c.title); got != c.want {
				t.Errorf("slugify(%q) = %q, want %q", c.title, got, c.want)
			}

			if len(c.want) > slugMax {
				t.Errorf("case expects %d characters, over the %d cut", len(c.want), slugMax)
			}
		})
	}
}

// The title comes from the host's own issue, through the one field gh is
// asked for and the whole issue glab answers with.
func TestHostIssueTitle(t *testing.T) {
	f := newFakes(t)
	f.script("gh issue view 1 --json title,url", `{"title":"Add dark mode","url":"https://github.com/o/r/issues/1"}`)
	f.script("glab issue view 1 -F json", `{"iid":1,"title":"Dunkelmodus","web_url":"https://x"}`)
	f.fail("gh issue view 2 --json title,url", shell.Result{Code: 1, Stderr: "no issue 2"})
	f.script("gh issue view 3 --json title,url", `{"title":""}`)
	f.script("gh issue view 4 --json title,url", `{"title":"Merged PR","url":"https://github.com/o/r/pull/4"}`)

	for name, want := range map[string]string{"gh": "Add dark mode", "glab": "Dunkelmodus"} {
		got, err := newHost(name, "/code/repo").issueTitle(t.Context(), "1")
		if err != nil || got != want {
			t.Errorf("%s title = %q, %v; want %q", name, got, err, want)
		}
	}

	gh := newHost("gh", "/code/repo")

	if _, err := gh.issueTitle(t.Context(), "2"); err == nil || !strings.Contains(err.Error(), "no issue 2") {
		t.Errorf("err = %v, want gh's stderr", err)
	}

	if _, err := gh.issueTitle(t.Context(), "3"); err == nil || !strings.Contains(err.Error(), "no title") {
		t.Errorf("err = %v, want no title", err)
	}

	// gh answers `issue view` for a pull request too: its title would name a
	// branch after an unrelated PR
	if _, err := gh.issueTitle(t.Context(), "4"); err == nil || !strings.Contains(err.Error(), "pull request") {
		t.Errorf("err = %v, want a pull request refused", err)
	}
}

// task read and task open refuse a pull request the same way.
func TestHostRefusesPullRequests(t *testing.T) {
	f := newFakes(t)
	pr := `{"number":35,"title":"Merged PR","url":"https://github.com/o/r/pull/35"}`
	f.script("gh issue view 35 --json number,title,state,labels,createdAt,url,body,comments", pr)
	f.script("gh issue view 35 --json url", pr)
	f.script("glab issue view 7 -F json", `{"iid":7,"web_url":"https://gitlab.com/g/p/-/merge_requests/7"}`)

	gh := newHost("gh", "/code/repo")

	if _, _, err := gh.issueDoc(
		t.Context(),
		"FT-35",
		"35",
		"",
	); err == nil ||
		!strings.Contains(err.Error(), "#35 is a pull request") {
		t.Errorf("issueDoc = %v", err)
	}

	if _, err := gh.issueURL(t.Context(), "35"); err == nil || !strings.Contains(err.Error(), "pull request") {
		t.Errorf("issueURL = %v", err)
	}

	if _, err := newHost("glab", "/code/repo").issueURL(t.Context(), "7"); err == nil {
		t.Error("glab merge request taken for an issue")
	}
}

// task read puts the labels and the created date, in UTC, between state
// and url, whichever shape the host gives them in.
func TestHostIssueDocLabels(t *testing.T) {
	f := newFakes(t)
	f.script("gh issue view 12 --json number,title,state,labels,createdAt,url,body,comments",
		`{"number":12,"title":"Crash","state":"OPEN","labels":[{"name":"bug"},{"name":"draft"}],`+
			`"url":"https://github.com/o/r/issues/12","body":"boom"}`)
	f.script("gh issue view 13 --json number,title,state,labels,createdAt,url,body,comments",
		`{"number":13,"title":"Plain","state":"OPEN","labels":[],"url":"https://github.com/o/r/issues/13"}`)
	f.script("gh issue view 14 --json number,title,state,labels,createdAt,url,body,comments",
		`{"number":14,"title":"Old","state":"OPEN","labels":[{"name":"bug"}],"createdAt":"2026-09-20T23:30:00-02:00",`+
			`"url":"https://github.com/o/r/issues/14"}`)
	f.script("glab issue view 7 -F json",
		`{"iid":7,"title":"Crash","state":"opened","labels":["bug"],"created_at":"2026-10-01T08:00:00Z",`+
			`"web_url":"https://gitlab.com/g/p/-/issues/7"}`)

	tests := []struct {
		host   string
		number string
		stale  string
		want   string
	}{
		{"gh", "12", "", "OPEN · bug, draft · https://github.com/o/r/issues/12\n"},
		{"gh", "13", "3w", "OPEN · https://github.com/o/r/issues/13\n"},
		{"gh", "14", "", "OPEN · bug · created 2026-09-21 · https://github.com/o/r/issues/14\n"},
		{
			"gh",
			"14",
			"3w",
			"OPEN · bug · created 2026-09-21 (stale: older than 3w) · https://github.com/o/r/issues/14\n",
		},
		{"glab", "7", "", "opened · bug · created 2026-10-01 · https://gitlab.com/g/p/-/issues/7\n"},
		{"glab", "7", "520w", "opened · bug · created 2026-10-01 · https://gitlab.com/g/p/-/issues/7\n"},
	}

	for _, tt := range tests {
		doc, _, err := newHost(tt.host, "/code/repo").issueDoc(t.Context(), "FT-"+tt.number, tt.number, tt.stale)
		if err != nil || !strings.Contains(doc, tt.want) {
			t.Errorf("%s issueDoc(%s) = %q, %v; want a line %q", tt.host, tt.number, doc, err, tt.want)
		}
	}
}

func TestTaskBranch(t *testing.T) {
	t.Run("local branch wins", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, heads("GH-7/thing"))

		got, err := taskBranch(t.Context(), "/code/repo", "GH-7")
		if err != nil || got != "GH-7/thing" {
			t.Errorf("taskBranch = %q, %v", got, err)
		}

		if f.called("git for-each-ref --format=%(refname) refs/remotes") {
			t.Error("origin was searched although a local branch matched")
		}
	})

	t.Run("origin is the fallback", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, "")
		f.script(refsAfterO, "refs/remotes/origin/GH-7")

		got, err := taskBranch(t.Context(), "/code/repo", "GH-7")
		if err != nil || got != "origin/GH-7" {
			t.Errorf("taskBranch = %q, %v", got, err)
		}
	})

	t.Run("nothing anywhere fails", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, "")
		f.script(refsAfterO, "")

		if _, err := taskBranch(t.Context(), "/code/repo", "GH-7"); err == nil {
			t.Error("taskBranch found a branch where none exists")
		}
	})

	t.Run("a branch carrying the id past a prefix is found before origin", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, "")
		f.script(allHeads, heads("main", "feat/GH-7-base"))

		got, err := taskBranch(t.Context(), "/code/repo", "GH-7")
		if err != nil || got != "feat/GH-7-base" {
			t.Errorf("taskBranch = %q, %v", got, err)
		}

		if f.called("git for-each-ref --format=%(refname) refs/remotes") {
			t.Error("origin was searched although a local branch matched")
		}
	})

	t.Run("several is an error naming them", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsAfter, heads("GH-7", "GH-7-attempt2"))

		_, err := taskBranch(t.Context(), "/code/repo", "GH-7")
		if err == nil || !strings.Contains(err.Error(), "GH-7-attempt2") {
			t.Errorf("err = %v, want the candidates named", err)
		}
	})
}

func TestNamedRef(t *testing.T) {
	t.Run("local branch wins", func(t *testing.T) {
		f := newFakes(t)
		f.script(shaLocal, "aaa")

		got, sha, err := namedRef(t.Context(), "/code/repo", "release/1.2")
		if err != nil || got != "release/1.2" || sha != "aaa" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}

		if f.called("git rev-parse --verify --quiet refs/remotes") {
			t.Error("remotes were searched although a local branch matched")
		}
	})

	t.Run("a remote typed as such is taken literally", func(t *testing.T) {
		f := newFakes(t)
		f.fail("git rev-parse --verify --quiet refs/heads/origin/main", missing)
		f.script("git rev-parse --verify --quiet refs/remotes/origin/main", "bbb")

		got, sha, err := namedRef(t.Context(), "/code/repo", "origin/main")
		if err != nil || got != "origin/main" || sha != "bbb" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}
	})

	t.Run("origin is the fallback", func(t *testing.T) {
		f := newFakes(t)
		f.fail(shaLocal, missing)
		f.fail(shaRemote, missing)
		f.script(shaOrigin, "ccc")

		got, sha, err := namedRef(t.Context(), "/code/repo", "release/1.2")
		if err != nil || got != "origin/release/1.2" || sha != "ccc" {
			t.Errorf("namedRef = %q, %q, %v", got, sha, err)
		}
	})

	t.Run("nothing anywhere fails naming the branch", func(t *testing.T) {
		f := newFakes(t)
		f.fail(shaLocal, missing)
		f.fail(shaRemote, missing)
		f.fail(shaOrigin, missing)

		_, _, err := namedRef(t.Context(), "/code/repo", "release/1.2")
		if err == nil || !strings.Contains(err.Error(), "release/1.2") {
			t.Errorf("err = %v, want the branch named", err)
		}
	})
}

func TestExistingBranch(t *testing.T) {
	cases := []struct {
		name    string
		refs    string
		heads   string // every local branch, read when refs finds none
		linked  string // the branches' f10-task links, as git config lists them
		want    string
		suffix  string
		got     string
		wantErr bool
	}{
		{name: "none", refs: "", want: "GH-1", got: ""},
		{name: "the intended name wins", refs: heads("GH-1-attempt2", "GH-1"), want: "GH-1", got: "GH-1"},
		{
			name: "one task branch is reused whatever its shape",
			refs: heads("GH-1/old-slug"),
			want: "GH-1/new-slug",
			got:  "GH-1/old-slug",
		},
		{
			name:   "a suffix narrows",
			refs:   heads("GH-1", "GH-1-attempt2", "GH-1/slug-attempt2"),
			want:   "GH-1-attempt2",
			suffix: "attempt2",
			got:    "GH-1-attempt2",
		},
		{
			name:   "a suffix with no match creates",
			refs:   heads("GH-1", "GH-1-attempt2"),
			want:   "GH-1-v3",
			suffix: "v3",
			got:    "",
		},
		{name: "several without the intended name", refs: heads("GH-1/a", "GH-1/b"), want: "GH-1", wantErr: true},
		{
			name:  "the id past a prefix",
			heads: heads("main", "feat/GH-1-workdays"),
			want:  "GH-1/workdays",
			got:   "feat/GH-1-workdays",
		},
		{
			name:  "a longer id is another task",
			heads: heads("feat/GH-10-other", "fix/gh-12"),
			want:  "GH-1",
			got:   "",
		},
		{
			name:  "the strict shapes win over a prefixed branch",
			refs:  heads("GH-1/slug"),
			heads: heads("GH-1/slug", "feat/GH-1-old"),
			want:  "GH-1/other",
			got:   "GH-1/slug",
		},
		{
			name:   "a prefixed branch linked to another task",
			heads:  heads("feat/GH-1-followup"),
			linked: "branch.feat/GH-1-followup.f10-task GH-5",
			want:   "GH-1",
			got:    "",
		},
		{
			name:   "a suffix narrows prefixed branches",
			heads:  heads("feat/GH-1-a", "feat/GH-1-a-attempt2"),
			want:   "GH-1-attempt2",
			suffix: "attempt2",
			got:    "feat/GH-1-a-attempt2",
		},
		{
			name:    "several prefixed branches",
			heads:   heads("feat/GH-1-a", "fix/GH-1-b"),
			want:    "GH-1",
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakes(t)
			f.script(refsLocal, c.refs)
			f.script(allHeads, c.heads)

			if c.linked != "" {
				f.script(linkedKeys, c.linked)
			}

			got, err := existingBranch(t.Context(), "/code/repo", c.want, "GH-1", c.suffix)
			if c.wantErr {
				if err == nil {
					t.Fatalf("existingBranch = %+v, want an error", got)
				}

				for name := range strings.FieldsSeq(strings.ReplaceAll(c.refs+c.heads, "refs/heads/", "")) {
					if !strings.Contains(err.Error(), name) {
						t.Errorf("err = %v, want %s named", err, name)
					}
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got.name != c.got || got.remote {
				t.Errorf("existingBranch = %+v, want %q, local", got, c.got)
			}
		})
	}
}

func TestPrompt(t *testing.T) {
	local, direct := shipFlow{name: "local"}, shipFlow{name: "direct", planless: true}

	for _, c := range []struct {
		mode string
		fl   shipFlow
		want string
	}{
		{modeDefault, shipFlow{}, "/f10:plan GH-1 && /f10:ship GH-1 --driven"},
		{modePlan, shipFlow{}, "/f10:plan GH-1 --driven"},
		{modeDefault, local, "/f10:plan GH-1 && /f10:ship local: GH-1 --driven"},
		{modeDefault, direct, "/f10:ship direct: GH-1 --driven"}, // a planless flow never plans
	} {
		if got := prompt(c.mode, c.fl, "GH-1", nil, "", nil, ""); got != c.want {
			t.Errorf("prompt(%q, %+v) = %q, want %q", c.mode, c.fl, got, c.want)
		}
	}

	dep := &dependency{
		task:    "GH-7",
		branch:  "GH-7/base",
		plan:    "/code/repo.GH-7-base/.f10/plans/GH-7.md",
		planned: true,
	}
	want := "/f10:plan GH-1 && GH-1 depends on GH-7, whose plan is at " +
		"/code/repo.GH-7-base/.f10/plans/GH-7.md --driven"

	if got := prompt(modePlan, shipFlow{}, "GH-1", dep, "", nil, ""); got != want {
		t.Errorf("prompt with a dependency = %q, want %q", got, want)
	}

	// a saved plan is shipped, except under --plan, which plans again
	plan := "/code/repo.GH-1/.f10/plans/GH-1.md"
	for _, c := range []struct {
		mode string
		fl   shipFlow
		want string
	}{
		{modeDefault, shipFlow{}, "/f10:ship GH-1 --driven"},
		{modePlan, shipFlow{}, "/f10:plan GH-1 --driven"},
		{modeDefault, local, "/f10:ship local: GH-1 --driven"},
	} {
		if got := prompt(c.mode, c.fl, "GH-1", nil, plan, nil, ""); got != c.want {
			t.Errorf("prompt(%q, %+v) with a plan = %q, want %q", c.mode, c.fl, got, c.want)
		}
	}

	want = "/f10:ship GH-1 && GH-1 depends on GH-7, whose plan is at " +
		"/code/repo.GH-7-base/.f10/plans/GH-7.md --driven"

	if got := prompt(modeDefault, shipFlow{}, "GH-1", dep, plan, nil, ""); got != want {
		t.Errorf("prompt with a plan and a dependency = %q, want %q", got, want)
	}
}

// The discuss prompt is plain text the user follows up on in the agent's
// pane, so it never carries the driven marker.
func TestDiscussPrompt(t *testing.T) {
	got := prompt(modeDiscuss, shipFlow{}, "GH-1", nil, "", nil, "")
	want := "Run `f10 task read GH-1` and summarise what it asks in a few lines. Then stop and wait."

	if got != want {
		t.Errorf("prompt(discuss) = %q, want %q", got, want)
	}

	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: "/plans/GH-7.md", planned: true}

	got = prompt(modeDiscuss, shipFlow{}, "GH-1", dep, "", nil, "")
	if !strings.Contains(got, "For context: GH-1 depends on GH-7, whose plan is at /plans/GH-7.md.") {
		t.Errorf("prompt(discuss) with a dependency = %q, want the dependency named", got)
	}

	if strings.Contains(got, drivenMarker) {
		t.Errorf("prompt(discuss) = %q, want no driven marker", got)
	}

	got = prompt(modeDiscuss, shipFlow{}, "GH-1", nil, "/plans/GH-1.md", nil, "")
	if !strings.Contains(got, "A plan for it is saved at /plans/GH-1.md; read it too.") {
		t.Errorf("prompt(discuss) with a plan = %q, want the plan named", got)
	}

	if strings.Contains(got, drivenMarker) {
		t.Errorf("prompt(discuss) with a plan = %q, want no driven marker", got)
	}
}

func TestPromptNote(t *testing.T) {
	got := prompt(modeDiscuss, shipFlow{}, "GH-1", nil, "", nil, "/f10:judge it first.")
	want := "Run `f10 task read GH-1` and summarise what it asks in a few lines. " +
		"Then, as the user asks: /f10:judge it first. Then stop and wait."

	if got != want {
		t.Errorf("prompt(discuss) with a note = %q, want %q", got, want)
	}

	pick := &pickup{url: "https://github.com/o/r/pull/9", view: "f10 pr view --comments 9", diff: "f10 pr diff 9"}
	if got := prompt(modeDefault, shipFlow{}, "GH-1", nil, "", pick, "check the tests"); !strings.HasSuffix(got,
		" Then, as the user asks: check the tests. Then stop and wait.") {
		t.Errorf("prompt for a PR with a note = %q, want the note before the wait", got)
	}
}

func TestRefuseNote(t *testing.T) {
	pick := &pickup{url: "https://github.com/o/r/pull/9"}

	for _, tc := range []struct {
		mode   string
		pick   *pickup
		note   string
		refuse bool
	}{
		{modeDefault, nil, "", false},
		{modeDiscuss, nil, "judge it first", false},
		{modeDefault, pick, "judge it first", false},
		{modeDefault, nil, "judge it first", true},
		{modePlan, nil, "judge it first", true},
		{modePlan, pick, "judge it first", true},
	} {
		if err := refuseNote(tc.mode, tc.pick, tc.note); (err != nil) != tc.refuse {
			t.Errorf(
				"refuseNote(%q, pick %v, %q) = %v, want refused %v",
				tc.mode,
				tc.pick != nil,
				tc.note,
				err,
				tc.refuse,
			)
		}
	}
}

// writePlan saves a plan file at path, making its dir.
func writePlan(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSiblingPathAndAgentName(t *testing.T) {
	if got, want := siblingPath("/code/repo", "GH-1/slug"), "/code/repo.GH-1-slug"; got != want {
		t.Errorf("siblingPath = %q, want %q", got, want)
	}

	if got, want := agentctl.BareAgentName("GH-1", ""), "gh-1"; got != want {
		t.Errorf("bareAgentName = %q, want %q", got, want)
	}

	if got, want := agentctl.BareAgentName("GH-1", "Attempt2"), "gh-1-attempt2"; got != want {
		t.Errorf("bareAgentName = %q, want %q", got, want)
	}

	// the same task in two checkouts gets two names, each in herdr's shape
	valid := regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	one, other := agentctl.AgentName("GH-1", "", "/code/repo.GH-1"), agentctl.AgentName("GH-1", "", "/code/other.GH-1")

	if one == other || !strings.HasPrefix(one, "gh-1-") || !valid.MatchString(one) {
		t.Errorf("agentName = %q and %q, want two distinct gh-1-<hash> names", one, other)
	}

	if long := agentctl.AgentName("ABCDEFGH-123456", "a-very-long-attempt-suffix", "/p"); !valid.MatchString(long) {
		t.Errorf("agentName = %q (%d chars), want at most 32", long, len(long))
	}
}

func TestResolveShipFlow(t *testing.T) {
	eff := &facts.Effective{Flows: map[string][]string{
		"default": {"plan", "implement", "review:local", "pr"},
		"local":   {"plan", "implement", "commit"},
		"direct":  {"implement", "push"},
	}}

	for name, want := range map[string]shipFlow{
		"":       {},
		"Local":  {name: "local"},
		"direct": {name: "direct", planless: true},
	} {
		if got, err := resolveShipFlow(eff, name); err != nil || got != want {
			t.Errorf("resolveShipFlow(%q) = %+v, %v; want %+v", name, got, err, want)
		}
	}

	// an unknown name fails with the list f10 flow prints
	_, err := resolveShipFlow(eff, "fast")
	if err == nil || !strings.Contains(err.Error(), "declares no such flow") ||
		!strings.Contains(err.Error(), "implement push") || !strings.Contains(err.Error(), "--flow direct") {
		t.Errorf("unknown flow = %v", err)
	}
}

// The whole flow with worktrunk present: wt creates, git is asked for the
// path, herdr opens, starts and prompts, and the report carries the facts.
func TestStartWithWorktrunk(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1")) // git knows the worktree once wt ran
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"task:", "GH-1", "branch:", "path:", "/code/repo.GH-1", "workspace:", "ws:3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}

	if f.called("git worktree add") {
		t.Error("git created the worktree although wt is installed")
	}

	if f.called("git config branch.GH-1." + cfgAfter) {
		t.Error("a dependency was recorded although --after was not given")
	}

	if strings.Contains(strings.Join(f.calls, "\n"), "--wait") {
		t.Error("the prompt waited on the agent")
	}
}

// A fresh start moves the task to its started status; a failure is a note
// beneath the report, never a refusal.
func TestStartMarksStarted(t *testing.T) {
	for name, fail := range map[string]bool{"moved": false, "failed": true} {
		f := newFakes(t)
		f.has["wt"] = true
		f.script(refsLocal, "")
		f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
		f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
		f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
		f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")
		f.script(
			"herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""),
			"{}",
		)

		var moved []string

		started := func(_ context.Context, task ref.Ref) error {
			moved = append(moved, task.ID)
			if fail {
				return errors.New("notion: 503")
			}

			return nil
		}

		var out bytes.Buffer

		if err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, started: started}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if len(moved) != 1 || moved[0] != "GH-1" {
			t.Errorf("%s: moved %v, want GH-1", name, moved)
		}

		if got := strings.Contains(out.String(), "not moved to its started status: notion: 503"); got != fail {
			t.Errorf("%s: note shown = %v:\n%s", name, got, out.String())
		}
	}
}

// Without worktrunk, git creates the worktree at the sibling path; --after
// swaps the base and records the dependency, and a base with no checkout
// here is named without a plan path, the report saying why.
func TestStartWithGitAlone(t *testing.T) {
	f := newFakes(t)
	f.script(refsLocal, "")
	f.script(refsAfter, heads("GH-7/base"))
	f.script(shaAfter, "ccc")
	f.script(headRef, "origin/main")
	f.script(shaMain, "bbb")
	f.script(cfgTask, "")
	f.script(cfgBranch, "")
	f.script("git merge-base refs/heads/GH-1 refs/heads/GH-7/base", "ccc")
	f.script("git config branch.GH-1."+cfgTip+" ccc", "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("git worktree add -b GH-1 /code/repo.GH-1 GH-7/base", "")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")

	dep := &dependency{task: "GH-7", branch: "GH-7/base"}
	local := shipFlow{name: "local"}
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, local, "GH-1", dep, "", nil, ""), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, flow: local, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if f.called("wt ") {
		t.Error("wt was run although it is not installed")
	}

	if !f.called("git config branch.GH-1." + cfgFlow + " local") {
		t.Error("the flow was not recorded on the branch")
	}

	if !f.called(cfgTask) || !f.called(cfgBranch) || !f.called("git config branch.GH-1."+cfgTip+" ccc") {
		t.Errorf("the dependency was not recorded: %v", f.calls)
	}

	// the branch exists before anything is recorded about it
	created := indexAfter(f.calls, "git worktree add -b GH-1 /code/repo.GH-1 GH-7/base", -1)
	if created < 0 || indexAfter(f.calls, cfgTask, created) < 0 || indexAfter(f.calls, cfgBranch, created) < 0 {
		t.Errorf("the dependency was recorded before the worktree existed: %v", f.calls)
	}

	for _, want := range []string{"after:", "GH-7", "GH-7 has no checkout here"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), "same commit") {
		t.Errorf("a base that differs from the default was noted as equal:\n%s", out.String())
	}
}

// afterFixture scripts a worktrunk run of `start --after GH-7` where GH-7's
// branch is checked out at dir, and returns the fakes to add the prompt to.
func afterFixture(t *testing.T, dir, shaBase string) *fakes {
	t.Helper()

	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(refsAfter, heads("GH-7/base"))
	f.script(shaAfter, shaBase)
	f.script(headRef, "origin/main")
	f.script(shaMain, "bbb")
	f.script(cfgTask, "")
	f.script(cfgBranch, "")
	f.script(worktrees, porcelain("GH-7/base", dir), porcelain("GH-7/base", dir, "GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create --base GH-7/base GH-1", "{}")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")

	return f
}

// A base task's plan found only in its own worktree, from before storage
// moved to main's, is the contract the prompt cites.
func TestStartAfterCitesThePlan(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, ".f10", "plans", "GH-7.md")

	if err := os.MkdirAll(filepath.Dir(plan), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(plan, []byte("# GH-7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := afterFixture(t, dir, "ccc")
	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: plan, planned: true}
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", dep, "", nil, ""), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if !f.called("herdr agent prompt " + agentOf("GH-1") + " ") {
		t.Errorf("the agent was not prompted: %v", f.calls)
	}

	if strings.Contains(out.String(), "no plan yet") || strings.Contains(out.String(), "out of reach") {
		t.Errorf("a plan that exists was reported missing:\n%s", out.String())
	}
}

// A base task whose plan is not written yet is named with the path the plan
// is expected at, and the report says it is missing.
func TestStartAfterWithoutAPlanYet(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, ".f10", "plans", "GH-7.md")

	f := afterFixture(t, dir, "ccc")
	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: plan}
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", dep, "", nil, ""), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if want := "GH-7 has no plan yet at " + plan; !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// Out-of-tree storage shares one plans dir, so the plan is reachable
// whether or not the base task has a checkout.
func TestStartAfterOutOfTreePlan(t *testing.T) {
	plans := t.TempDir()
	plan := filepath.Join(plans, "GH-7.md")

	if err := os.WriteFile(plan, []byte("# GH-7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := afterFixture(t, "/code/repo.GH-7-base", "ccc")
	addDir := " -- " + strings.Join(agentArgs(agentctl.Profile{}, plans, "/code/repo.GH-1"), " ")
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7"+addDir, "{}")
	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: plan, planned: true}
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", dep, "", nil, ""), "{}")

	err := start(
		t.Context(),
		&bytes.Buffer{},
		startInput{main: "/code/repo", task: gh1, after: "GH-7", plansDir: plans},
	)
	if err != nil {
		t.Fatal(err)
	}
}

// A base task whose branch still sits at the default branch's commit has no
// code yet, and the note says so beneath the report.
func TestStartAfterEqualsDefault(t *testing.T) {
	f := afterFixture(t, "/code/repo.GH-7-base", "bbb")
	dep := &dependency{task: "GH-7", branch: "GH-7/base", plan: "/code/repo.GH-7-base/.f10/plans/GH-7.md"}
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", dep, "", nil, ""), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	want := "--after GH-7: branch GH-7/base is at the same commit as the default branch origin/main, so GH-7 has no code yet"
	if !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// A --after that points to no branch refuses before git, wt or herdr
// create or record anything.
func TestStartRefusesMissingAfter(t *testing.T) {
	f := newFakes(t)
	f.script(refsAfter, "")
	f.script(refsAfterO, "")

	err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1, after: "GH-7"})
	if err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Fatalf("err = %v, want the missing base refused", err)
	}

	if f.called("wt ") || f.called("git worktree add") || f.called("git config branch.") || f.called("herdr") {
		t.Errorf("something was created after the refusal: %v", f.calls)
	}
}

// --base hands git the ref it found, and a base that is not at the
// default branch's commit gets no note.
func TestStartWithBase(t *testing.T) {
	f := newFakes(t)
	f.script(shaLocal, "aaa")
	f.script(refsLocal, "")
	f.script(headRef, "origin/main")
	f.script(shaMain, "bbb")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("git worktree add -b GH-1 /code/repo.GH-1 release/1.2", "")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, base: "release/1.2"})
	if err != nil {
		t.Fatal(err)
	}

	if !f.called("git worktree add -b GH-1 /code/repo.GH-1 release/1.2") {
		t.Error("the worktree was not created from the base")
	}

	if strings.Contains(out.String(), "same commit") {
		t.Errorf("a base that differs from the default was noted as equal:\n%s", out.String())
	}
}

// A base at the default branch's commit still creates the worktree from
// it, and says beneath the report that it changed nothing.
func TestStartBaseEqualsDefault(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script("git rev-parse --verify --quiet refs/heads/main", "aaa")
	f.script(refsLocal, "")
	f.script(headRef, "origin/main")
	f.script(shaMain, "aaa")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create --base main GH-1", "{}")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, base: "main"})
	if err != nil {
		t.Fatal(err)
	}

	if !f.called("wt switch --no-cd --yes --format json --create --base main GH-1") {
		t.Error("the worktree was not created from the base")
	}

	want := "--base main is at the same commit as the default branch origin/main"
	if !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// A task that already has a branch keeps its base: --base is noted as
// ignored and never reaches wt.
func TestStartBaseIgnoredOnExistingBranch(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(shaLocal, "aaa")
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json GH-1", "{}")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""), "{}")

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, base: "release/1.2"})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "--base ignored") {
		t.Errorf("report missing the ignored note:\n%s", out.String())
	}

	if strings.Contains(strings.Join(f.calls, "\n"), "--base") {
		t.Error("the base reached wt although the branch exists")
	}
}

// A --base that points nowhere refuses before git, wt or herdr create anything.
func TestStartRefusesMissingBase(t *testing.T) {
	f := newFakes(t)
	f.fail(shaLocal, missing)
	f.fail(shaRemote, missing)
	f.fail(shaOrigin, missing)

	err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1, base: "release/1.2"})
	if err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Fatalf("err = %v, want the missing base refused", err)
	}

	if f.called("wt ") || f.called("git worktree add") || f.called("herdr") {
		t.Errorf("something was created after the refusal: %v", f.calls)
	}
}

// A second run for the same task opens the worktree that exists: Herdr
// already shows it, so no agent is started or prompted. --after is noted as
// ignored for the base, which the branch keeps, and the dependency recorded.
func TestStartReusesTheWorktreeAndWorkspace(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(refsAfter, heads("GH-7"))
	f.script("git rev-parse --verify --quiet GH-7", "ccc")
	f.script("git config branch.GH-1.f10-after GH-7", "")
	f.script("git config branch.GH-1.f10-after-branch GH-7", "")
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", reopened)
	f.script("herdr agent get pane:7", agentFound)

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, mode: modePlan, after: "GH-7"})
	if err != nil {
		t.Fatal(err)
	}

	if f.called("wt ") || f.called("git worktree add") {
		t.Error("a worktree was created for a branch that has one")
	}

	if f.called("herdr agent start") || f.called("herdr agent prompt") {
		t.Error("an agent was started in a workspace that already had one")
	}

	if !f.called("git config branch.GH-1.f10-after-branch GH-7") {
		t.Error("the dependency was not recorded on the existing branch")
	}

	for _, want := range []string{
		"reused the existing worktree",
		"--after ignored for the base",
		"dependency on GH-7 recorded",
		"workspace already open",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// A second start on a checkout Herdr already shows adopts that workspace
// instead of asking Herdr to open it, which can answer with a duplicate.
func TestStartAdoptsTheWorkspaceHerdrShows(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr workspace list", `{"result":{"workspaces":[`+
		`{"workspace_id":"ws:3","label":"GH-1","worktree":{"checkout_path":"/code/repo.GH-1"}}]}}`)
	f.script("herdr pane list --workspace ws:3", `{"result":{"panes":[{"pane_id":"pane:7","agent":"claude"}]}}`)
	f.script("herdr agent get pane:7", agentFound)

	var out bytes.Buffer

	if err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1}); err != nil {
		t.Fatal(err)
	}

	if f.called("herdr worktree open") || f.called("herdr agent start") || f.called("herdr agent prompt") {
		t.Errorf("calls = %q", f.calls)
	}

	for _, want := range []string{"ws:3", "workspace already open"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// A workspace left open by a start that failed on a busy pane has no agent:
// its idle root shell gets one, and the agent gets its prompt.
func TestStartRecoversAWorkspaceWithNoAgent(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", reopened)
	f.fail("herdr agent get pane:7", shell.Result{
		Code:   1,
		Stderr: `{"error":{"code":"agent_not_found","message":"agent target gh-1 not found"}}`,
	})
	f.script("herdr pane process-info --pane pane:7",
		`{"result":{"process_info":{"foreground_process_group_id":42,"pane_id":"pane:7","shell_pid":42}}}`)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""), "{}")

	var out bytes.Buffer

	if err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1}); err != nil {
		t.Fatal(err)
	}

	if !f.called("herdr agent prompt " + agentOf("GH-1")) {
		t.Error("the recovered agent was not prompted")
	}

	if want := "started one in its idle shell"; !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// A workspace with no agent whose root pane runs something else is the
// user's: nothing is started there and nothing is prompted.
func TestStartLeavesABusyPaneAlone(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", reopened)
	f.fail("herdr agent get pane:7", shell.Result{
		Code:   1,
		Stderr: `{"error":{"code":"agent_not_found","message":"agent target gh-1 not found"}}`,
	})
	f.script("herdr pane process-info --pane pane:7",
		`{"result":{"process_info":{"foreground_process_group_id":77,"pane_id":"pane:7","shell_pid":42}}}`)

	var out bytes.Buffer

	if err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1}); err != nil {
		t.Fatal(err)
	}

	if f.called("herdr agent start") || f.called("herdr agent prompt") {
		t.Error("an agent was started over what the pane runs")
	}

	if want := "runs something other than an idle shell"; !strings.Contains(out.String(), want) {
		t.Errorf("report missing %q:\n%s", want, out.String())
	}
}

// A fresh pane whose shell is still starting answers busy; start waits it
// out and prompts the agent once it starts.
func TestStartWaitsForTheShell(t *testing.T) {
	prevPoll := herdr.StartPoll
	herdr.StartPoll = time.Millisecond

	t.Cleanup(func() { herdr.StartPoll = prevPoll })

	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.answers["herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7"] = []shell.Result{
		{
			Code:   1,
			Stderr: `{"error":{"code":"agent_pane_busy","message":"agent target pane pane:7 is not an available shell"}}`,
		},
		{Stdout: "{}"},
	}
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""), "{}")

	var progress bytes.Buffer

	if err := start(
		t.Context(),
		io.Discard,
		startInput{main: "/code/repo", task: gh1, progress: &progress},
	); err != nil {
		t.Fatal(err)
	}

	if !f.called("herdr agent prompt " + agentOf("GH-1")) {
		t.Error("the agent was not prompted once its pane was free")
	}

	if want := "waiting for the shell in pane pane:7 to finish starting"; !strings.Contains(progress.String(), want) {
		t.Errorf("progress = %q, want %q", progress.String(), want)
	}
}

// The default branch carries the title: the worktree lands at the
// flattened sibling path, the workspace is labelled with the full name,
// and the agent keeps the task's name.
func TestStartSlugsTheBranch(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1/add-dark-mode", "/code/repo.GH-1-add-dark-mode"))
	f.script("wt switch --no-cd --yes --format json --create GH-1/add-dark-mode", "{}")
	f.script(
		"herdr worktree open --cwd /code/repo --path /code/repo.GH-1-add-dark-mode --label GH-1/add-dark-mode",
		opened,
	)
	f.script(
		"herdr agent start "+agentctl.AgentName(
			"GH-1",
			"",
			"/code/repo.GH-1-add-dark-mode",
		)+" --kind claude --pane pane:7",
		"{}",
	)
	f.script(
		"herdr agent prompt "+agentctl.AgentName(
			"GH-1",
			"",
			"/code/repo.GH-1-add-dark-mode",
		)+" "+prompt(
			modeDefault,
			shipFlow{},
			"GH-1",
			nil,
			"",
			nil,
			"",
		),
		"{}",
	)

	var out bytes.Buffer

	err := start(t.Context(), &out, startInput{main: "/code/repo", task: gh1, title: titled("Add dark mode", nil)})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"branch:", "GH-1/add-dark-mode", "/code/repo.GH-1-add-dark-mode"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), "bare id") {
		t.Errorf("a slugged branch was reported as the bare id:\n%s", out.String())
	}
}

// No title from the host still starts the task, on the bare id, and the
// report says why beneath the block.
func TestStartWithoutATitle(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, "")
	f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	f.script("wt switch --no-cd --yes --format json --create GH-1", "{}")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""), "{}")

	var out bytes.Buffer

	in := startInput{main: "/code/repo", task: gh1, title: titled("", errors.New("gh: offline"))}
	if err := start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "no title for GH-1 (gh: offline), so the branch is the bare id") {
		t.Errorf("report missing the bare-id note:\n%s", out.String())
	}
}

// A task with a bare-id branch keeps it over the slug: git cannot hold GH-1
// and GH-1/<slug> at once, and the existing one is the work. No bare-id
// note either, since this run named nothing.
func TestStartReusesABareBranchOverTheSlug(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", reopened)
	f.script("herdr agent get pane:7", agentFound)

	var out bytes.Buffer

	err := start(
		t.Context(),
		&out,
		startInput{main: "/code/repo", task: gh1, title: titled("", errors.New("gh: offline"))},
	)
	if err != nil {
		t.Fatal(err)
	}

	if f.called("wt ") {
		t.Error("a worktree was created although the task's branch has one")
	}

	if strings.Contains(out.String(), "bare id") {
		t.Errorf("the note explains a name that was not given:\n%s", out.String())
	}
}

// A worktree that exists but is not open in Herdr gets a fresh agent.
func TestStartOpensAClosedWorktree(t *testing.T) {
	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1", opened)
	f.script("herdr agent start "+agentOf("GH-1")+" --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt "+agentOf("GH-1")+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""), "{}")

	if err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1}); err != nil {
		t.Fatal(err)
	}

	if !f.called("herdr agent prompt " + agentOf("GH-1")) {
		t.Error("the agent was not prompted")
	}
}

// A plan main saved stays in main: the agent ships it in place, and is
// started with main's task-file dirs added, since they lie outside its
// worktree.
func TestStartReadsMainsPlanInPlace(t *testing.T) {
	main, wt := t.TempDir(), t.TempDir()
	plans := filepath.Join(main, ".f10", "plans")
	src := filepath.Join(plans, "GH-1.md")
	writePlan(t, src)

	agent := agentctl.AgentName("GH-1", "", wt)

	f := newFakes(t)
	f.has["wt"] = true
	f.script(refsLocal, heads("GH-1"))
	f.script(worktrees, porcelain("GH-1", wt))
	f.script("herdr worktree open --cwd "+main+" --path "+wt+" --label GH-1", opened)
	f.script("herdr agent start "+agent+" --kind claude --pane pane:7 -- "+
		strings.Join(agentArgs(agentctl.Profile{}, plans, wt), " "), "{}")
	f.script("herdr agent prompt "+agent+" "+prompt(modeDefault, shipFlow{}, "GH-1", nil, src, nil, ""), "{}")

	var out bytes.Buffer

	if err := start(t.Context(), &out, startInput{main: main, task: gh1, plansDir: plans}); err != nil {
		t.Fatal(err)
	}

	if !f.called("herdr agent prompt " + agent + " /f10:ship GH-1 --driven") {
		t.Errorf("the agent was not prompted to ship: %v", f.calls)
	}

	if _, err := os.Stat(src); err != nil {
		t.Errorf("main's plan moved: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wt, ".f10")); err == nil {
		t.Error("the worktree got a .f10 of its own")
	}

	if !strings.Contains(out.String(), src) {
		t.Errorf("report does not name the plan %s:\n%s", src, out.String())
	}
}

// An existing branch without a checkout gets one, without --create.
func TestStartChecksOutAnExistingBranch(t *testing.T) {
	f := newFakes(t)
	f.script(refsLocal, heads("GH-1/slug"))
	f.script(worktrees, porcelain(), porcelain("GH-1/slug", "/code/repo.GH-1-slug"))
	f.script("git worktree add /code/repo.GH-1-slug GH-1/slug", "")
	f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1-slug --label GH-1/slug", opened)
	f.script(
		"herdr agent start "+agentctl.AgentName("GH-1", "", "/code/repo.GH-1-slug")+" --kind claude --pane pane:7",
		"{}",
	)
	f.script(
		"herdr agent prompt "+agentctl.AgentName("GH-1", "", "/code/repo.GH-1-slug")+" "+
			prompt(modeDefault, shipFlow{}, "GH-1", nil, "", nil, ""),
		"{}",
	)

	if err := start(t.Context(), &bytes.Buffer{}, startInput{main: "/code/repo", task: gh1}); err != nil {
		t.Fatal(err)
	}

	if !f.called("git worktree add /code/repo.GH-1-slug GH-1/slug") {
		t.Error("the existing branch was not checked out")
	}
}

// Outside Herdr the command refuses before any git, wt or herdr call, and
// the message points at herdr.dev.
func TestStartRefusesOutsideHerdr(t *testing.T) {
	f := newFakes(t)
	t.Setenv("HERDR_ENV", "")

	var out bytes.Buffer

	app := newApp()
	app.Writer = &out

	err := app.Run(t.Context(), []string{"f10", "start", "42"})
	if !errors.Is(err, herdr.ErrNotInside) {
		t.Fatalf("err = %v, want ErrNotInside", err)
	}

	if !strings.Contains(err.Error(), "https://herdr.dev") {
		t.Errorf("the refusal does not name herdr.dev: %v", err)
	}

	if len(f.calls) != 0 {
		t.Errorf("commands ran before the refusal: %v", f.calls)
	}
}

func TestStartModesAreExclusive(t *testing.T) {
	newFakes(t)

	for _, flags := range [][]string{{"--plan", "--local"}, {"--discuss", "--plan"}, {"--local", "--discuss"}} {
		args := append(append([]string{"f10", "start"}, flags...), "42")

		err := newApp().Run(t.Context(), args)
		if err == nil || !strings.Contains(err.Error(), "exclusive") {
			t.Errorf("%v: err = %v, want the flags refused together", flags, err)
		}
	}
}

func TestStartBaseAndAfterAreExclusive(t *testing.T) {
	f := newFakes(t)

	err := newApp().Run(t.Context(), []string{"f10", "start", "--base", "main", "--after", "7", "42"})
	if err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Errorf("err = %v, want the flags refused together", err)
	}

	if len(f.calls) != 0 {
		t.Errorf("commands ran before the refusal: %v", f.calls)
	}
}

// A parent with unfinished subtasks is refused with exitParent, reading no
// more than whether the task has a branch; a started task, one whose subtasks
// are all finished and one with none start.
func TestStartRefusesAParent(t *testing.T) {
	kidsOf := func(rows []childRow, err error, asked *bool) func(context.Context, ref.Ref) ([]childRow, error) {
		return func(context.Context, ref.Ref) ([]childRow, error) {
			*asked = true

			return rows, err
		}
	}

	t.Run("open subtasks", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsLocal, "")

		var asked bool

		rows := []childRow{{ID: "GH-2"}, {ID: "GH-3", Finished: true}, {ID: "GH-4"}}
		in := startInput{main: "/code/repo", task: gh1, children: kidsOf(rows, nil, &asked)}
		err := refuseStartParent(t.Context(), in)

		var coder cli.ExitCoder
		if !errors.As(err, &coder) || coder.ExitCode() != exitParent {
			t.Fatalf("err = %v, want exit %d", err, exitParent)
		}

		for _, want := range []string{"GH-1 is a parent of GH-2, GH-4", "/f10:drive GH-1", "f10 start GH-2"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message missing %q: %v", want, err)
			}
		}

		if !asked {
			t.Error("the refusal did not come from the subtask lookup")
		}

		for _, c := range f.calls {
			if !strings.HasPrefix(c, "git for-each-ref") && !strings.HasPrefix(c, linkedKeys) {
				t.Errorf("%q ran for the check, which only looks for the task's branch", c)
			}
		}
	})

	for name, tc := range map[string]struct {
		rows []childRow
		err  error
	}{
		"every subtask finished": {rows: []childRow{{ID: "GH-2", Finished: true}}},
		"no subtasks":            {rows: []childRow{}},
		"no parent link":         {err: errNoParentLink},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakes(t)
			f.script(refsLocal, "")

			var asked bool

			in := startInput{main: "/code/repo", task: gh1, children: kidsOf(tc.rows, tc.err, &asked)}
			if err := refuseStartParent(t.Context(), in); err != nil || !asked {
				t.Errorf("err = %v, asked = %v; want a pass after asking", err, asked)
			}
		})
	}

	t.Run("a started task", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsLocal, heads("GH-1"))

		var asked bool

		in := startInput{main: "/code/repo", task: gh1, children: kidsOf([]childRow{{ID: "GH-2"}}, nil, &asked)}
		if err := refuseStartParent(t.Context(), in); err != nil || asked {
			t.Errorf("err = %v, asked = %v; a started task is never a parent", err, asked)
		}
	})

	t.Run("no check", func(t *testing.T) {
		newFakes(t)

		if err := refuseStartParent(t.Context(), startInput{main: "/code/repo", task: gh1}); err != nil {
			t.Errorf("err = %v with no children lookup", err)
		}
	})
}

// Every mode but --discuss refuses a parent; a PR pickup never does.
func TestChecksParent(t *testing.T) {
	for _, mode := range []string{modeDefault, modePlan} {
		if !checksParent(mode, nil) {
			t.Errorf("mode %q skips the parent check", mode)
		}
	}

	if checksParent(modeDiscuss, nil) {
		t.Error("--discuss refuses a parent")
	}

	if checksParent(modeDefault, pickGH) || checksParent(modePlan, pickGH) {
		t.Error("a PR pickup refuses a parent")
	}
}
