package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The board over a scope. These checkouts have no remote, so every host and
// tracker source fails and the local one carries the run.

// --all is the F10_ROOTS walk, so it is off until the variable is set.
func TestBoardAllNeedsRoots(t *testing.T) {
	testkit.IsolateEnv(t)
	t.Setenv(projects.RootsEnv, "")

	if _, err := runApp(t, "board", "--all").outErr(); err == nil || !strings.Contains(err.Error(), projects.RootsEnv) {
		t.Errorf("err = %v, want %s named", err, projects.RootsEnv)
	}
}

// One project whose instructions do not read is a board with every
// source failed; the other renders.
func TestBoardScopeFailsOneProjectAlone(t *testing.T) {
	testkit.IsolateEnv(t)

	root := testkit.Canonical(t, t.TempDir())
	good := testkit.ScopeRepo(t, root, "good")
	broken := testkit.ScopeRepo(t, root, "broken")
	testkit.Git(t, broken, "remote", "add", "origin", "git@github.com:acme/broken.git")

	instructions := filepath.Join(broken, ".f10", "instructions")
	if err := os.MkdirAll(instructions, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(instructions, "project.md"),
		[]byte("---\nbogus: 1\n---\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	t.Setenv(projects.RootsEnv, filepath.Join(root, "*"))

	out, err := runApp(t, "board", "--all", "--json").outErr()
	if err != nil {
		t.Fatal(err)
	}

	var boards []lookaround.Board
	if err := json.Unmarshal([]byte(out), &boards); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if len(boards) != 2 || boards[0].Project.Name != "broken" || boards[1].Project.Name != "good" {
		t.Fatalf("boards = %d, want broken then good: %s", len(boards), out)
	}

	for _, s := range boards[0].Sources {
		if s.OK || !strings.Contains(s.Error, "bogus") {
			t.Errorf("broken %s: %+v, want failed naming the key", s.Name, s)
		}
	}

	if p := boards[0].Project; p.ID != "github.com/acme/broken" || p.Checkout != broken {
		t.Errorf("broken project = %+v, want its id and main checkout", p)
	}

	if local := boards[1].Sources[2]; !local.OK || boards[1].Project.Checkout != good {
		t.Errorf("good: local %+v, checkout %q, want ok at %s", local, boards[1].Project.Checkout, good)
	}

	// the glance: a line per project, the broken one failed, the good one
	// local only with no failed source, its feed its branch committed today
	text, err := runApp(t, "board", "--all", "--state").outErr()
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "broken") || !strings.Contains(lines[0], "failed") ||
		!strings.HasPrefix(lines[1], "good") || !strings.Contains(lines[1], "nothing in flight") ||
		!strings.Contains(lines[1], "local only") || strings.Contains(lines[1], "failed") {
		t.Errorf("summary =\n%s", text)
	}

	feed, err := runApp(t, "board", "--all").outErr()
	if err != nil {
		t.Fatal(err)
	}

	lines = strings.Split(strings.TrimSpace(feed), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "today · ") ||
		!strings.HasPrefix(lines[2], "good") || !strings.Contains(lines[2], "1 updated  local only") {
		t.Errorf("feed summary =\n%s", feed)
	}
}

// -C alone shows every project's rows under its name; --by takes person.
func TestBoardCheckoutsExpand(t *testing.T) {
	testkit.IsolateEnv(t)

	root := testkit.Canonical(t, t.TempDir())
	a, b := testkit.ScopeRepo(t, root, "a"), testkit.ScopeRepo(t, root, "b")

	out, err := runApp(t, "-C", a, "-C", b, "board").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "## a") || !strings.Contains(out, "## b") {
		t.Errorf("output lacks a heading per project:\n%s", out)
	}

	// one -C is one project: one document, no array
	one, err := runApp(t, "-C", a, "board", "--json").outErr()
	if err != nil {
		t.Fatal(err)
	}

	var board lookaround.Board
	if err := json.Unmarshal([]byte(one), &board); err != nil || board.Project.Name != "a" {
		t.Errorf("one -C --json = %v, %.80s, want one board for a", err, one)
	}

	if _, err := runApp(t, "-C", a, "board", "--by", "team").outErr(); err == nil ||
		!strings.Contains(err.Error(), "person") {
		t.Errorf("--by team: err = %v, want person named", err)
	}
}

// A watched repository runs through the whole scope: --all adds it, the
// host is asked with the repository named, and one a checkout in the
// scope already covers is dropped. The host CLI is scripted; git is real.
func TestBoardAllReadsWatchedRepos(t *testing.T) {
	home := testkit.IsolateEnv(t)

	root := testkit.Canonical(t, t.TempDir())
	good := testkit.ScopeRepo(t, root, "good")
	testkit.Git(t, good, "remote", "add", "origin", "https://github.com/acme/good.git")

	testkit.WriteUserFile(
		t,
		home,
		scope.BoardConfigFile,
		"---\nrepos: [github.com/acme/good, github.com/acme/other]\n---\n",
	)
	t.Setenv(projects.RootsEnv, filepath.Join(root, "*"))

	var (
		mu    sync.Mutex // projects are collected concurrently
		calls []string
	)

	seam.Swap(t, &shell.Capture, func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		call := filepath.Base(name) + " " + strings.Join(args, " ")

		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()

		switch {
		case strings.HasPrefix(call, "gh issue list") && strings.HasSuffix(call, "-R acme/other"):
			return shell.Result{Stdout: `[{"number": 3, "title": "ship it", "state": "OPEN", ` +
				`"url": "https://github.com/acme/other/issues/3", "assignees": [{"login": "ann"}], ` +
				`"createdAt": "` + time.Now().UTC().Format(time.RFC3339) + `"}]`}, nil
		case strings.HasPrefix(call, "gh pr list"), strings.HasPrefix(call, "gh issue list"):
			return shell.Result{Stdout: "[]"}, nil
		case strings.HasPrefix(call, "gh api graphql --paginate"):
			return shell.Result{Stdout: `{"data":{"search":{"nodes":[]},"quiet":{"issueCount":2,"nodes":[]}}}`}, nil
		default:
			return shell.Result{Code: 1, Stderr: "unscripted: " + call}, nil
		}
	})

	out, err := runApp(t, "board", "--all", "--json", "--state").outErr()
	if err != nil {
		t.Fatal(err)
	}

	var boards []lookaround.Board
	if err := json.Unmarshal([]byte(out), &boards); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if len(boards) != 2 || boards[0].Project.ID != "github.com/acme/good" ||
		boards[1].Project.ID != "github.com/acme/other" {
		t.Fatalf("boards = %d, want good from its checkout then other: %s", len(boards), out)
	}

	if boards[0].Project.Checkout != good || boards[0].Project.Remote {
		t.Errorf("good = %+v, want read from its checkout", boards[0].Project)
	}

	other := boards[1]
	if other.Project.Checkout != "" || !other.Project.Remote || !other.Sources[2].Skipped ||
		len(other.Rows) != 1 || other.Rows[0].Key != "OTH-3" || other.Rows[0].Who.Person != "ann" {
		t.Errorf("other = %+v rows %+v, want remote, local skipped, one row by ann", other.Project, other.Rows)
	}

	for _, want := range []string{"gh pr list --state all --search is:open --json", "-R acme/other"} {
		if !slices.ContainsFunc(calls, func(c string) bool { return strings.Contains(c, want) }) {
			t.Errorf("no host call carrying %q:\n%s", want, strings.Join(calls, "\n"))
		}
	}

	// the feed asks the host once per repository, the repository named,
	// and finds the task created today
	calls = nil

	out, err = runApp(t, "board", "--all", "--json").outErr()
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal([]byte(out), &boards); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if f := boards[1].Feed; f == nil || len(f.Rows) != 1 || f.Rows[0].Group != lookaround.GroupNewTasks ||
		f.Rows[0].Who.Person != "ann" || f.Quiet.Count != 2 {
		t.Errorf("other's feed = %+v", f)
	}

	var graphql []string

	for _, c := range calls {
		if strings.HasPrefix(c, "gh api graphql") {
			graphql = append(graphql, c)
		}

		if strings.HasPrefix(c, "gh pr list") {
			t.Errorf("the feed listed requests: %s", c)
		}
	}

	if len(graphql) != 2 || !slices.ContainsFunc(graphql, func(c string) bool {
		return strings.Contains(c, "-F q=repo:acme/other is:pr updated:>=")
	}) {
		t.Errorf("graphql calls = %q, want one per repository, other named", graphql)
	}

	// a repository entry that does not parse fails only the run that reads it
	testkit.WriteUserFile(t, home, scope.BoardConfigFile, "---\nrepos: [git.example.com/acme/x]\n---\n")

	if _, err := runApp(t, "-C", good, "board", "--json").outErr(); err != nil {
		t.Errorf("one checkout with a bad repos entry: %v, want it unread", err)
	}

	if _, err := runApp(t, "board", "--all").outErr(); err == nil || !strings.Contains(err.Error(), "repos") {
		t.Errorf("--all with a bad repos entry: %v, want it named", err)
	}
}
