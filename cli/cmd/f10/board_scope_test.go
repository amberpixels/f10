package main

import (
	"bytes"
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
	"github.com/amberpixels/f10/cli/internal/shell"
)

// The board over a scope: every project on its own, one that cannot be
// read failing alone, the JSON an array of boards. These checkouts have
// no remote, so every host and tracker source fails and the local one
// carries the run; the host side of a scope is the repo-entry fixture's.

// --all is the F10_ROOTS walk, so it is off until the variable is set.
func TestBoardAllNeedsRoots(t *testing.T) {
	isolateEnv(t)
	t.Setenv(projects.RootsEnv, "")

	if _, err := runStandupApp(t, "board", "--all"); err == nil || !strings.Contains(err.Error(), projects.RootsEnv) {
		t.Errorf("err = %v, want %s named", err, projects.RootsEnv)
	}
}

// One project whose instructions do not read is a board with every
// source failed; the other renders.
func TestBoardScopeFailsOneProjectAlone(t *testing.T) {
	isolateEnv(t)

	root := canonical(t, t.TempDir())
	good := scopeRepo(t, root, "good")
	broken := scopeRepo(t, root, "broken")
	git(t, broken, "remote", "add", "origin", "git@github.com:acme/broken.git")

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

	out, err := runStandupApp(t, "board", "--all", "--json")
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
	text, err := runStandupApp(t, "board", "--all", "--state")
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "broken") || !strings.Contains(lines[0], "failed") ||
		!strings.HasPrefix(lines[1], "good") || !strings.Contains(lines[1], "nothing in flight") ||
		!strings.Contains(lines[1], "local only") || strings.Contains(lines[1], "failed") {
		t.Errorf("summary =\n%s", text)
	}

	feed, err := runStandupApp(t, "board", "--all")
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
	isolateEnv(t)

	root := canonical(t, t.TempDir())
	a, b := scopeRepo(t, root, "a"), scopeRepo(t, root, "b")

	out, err := runStandupApp(t, "-C", a, "-C", b, "board")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "## a") || !strings.Contains(out, "## b") {
		t.Errorf("output lacks a heading per project:\n%s", out)
	}

	// one -C is one project: one document, no array
	one, err := runStandupApp(t, "-C", a, "board", "--json")
	if err != nil {
		t.Fatal(err)
	}

	var board lookaround.Board
	if err := json.Unmarshal([]byte(one), &board); err != nil || board.Project.Name != "a" {
		t.Errorf("one -C --json = %v, %.80s, want one board for a", err, one)
	}

	if _, err := runStandupApp(
		t,
		"-C",
		a,
		"board",
		"--by",
		"team",
	); err == nil ||
		!strings.Contains(err.Error(), "person") {
		t.Errorf("--by team: err = %v, want person named", err)
	}
}

// A board group lists repositories beside paths; a repository on an
// unknown host says which prefix to write; a missing group names the
// board's own file.
func TestBoardGroupsMixRepositories(t *testing.T) {
	home := isolateEnv(t)

	root := canonical(t, t.TempDir())
	scopeRepo(t, root, "acme-api")

	writeUserFile(t, home, boardConfigFile, "---\ngroups:\n  work: ["+root+"/acme-*, github.com/acme/widgets]\n"+
		"  odd: [git.example.com/acme/widgets]\n---\n")

	items, err := readGroups(boardConfigFile, []string{"work"})
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 2 || items[0].dir != filepath.Join(root, "acme-api") || items[1].repo == nil ||
		items[1].repo.id() != "github.com/acme/widgets" || items[1].repo.CLI != "gh" || items[1].repo.Prefix != "WID" {
		t.Errorf("items = %+v", items)
	}

	if _, err := readGroups(boardConfigFile, []string{"odd"}); err == nil || !strings.Contains(err.Error(), "glab:") {
		t.Errorf("odd: err = %v, want the prefixes named", err)
	}

	if _, err := readGroups(
		boardConfigFile,
		[]string{"play"},
	); err == nil ||
		!strings.Contains(err.Error(), boardConfigFile) {
		t.Errorf("play: err = %v, want %s named", err, boardConfigFile)
	}

	// standup's groups live in its own file: the board's are not read for it
	if _, err := readGroups(standupConfigFile, []string{"work"}); err == nil ||
		!strings.Contains(err.Error(), standupConfigFile) {
		t.Errorf("standup: err = %v, want %s named", err, standupConfigFile)
	}
}

// A repository entry parses as a string or a map, and the map's prefix
// wins over the derived one.
func TestBoardRepos(t *testing.T) {
	home := isolateEnv(t)

	writeUserFile(t, home, boardConfigFile, "---\nrepos:\n  - github.com/acme/widgets\n"+
		"  - {repo: gitlab.com/acme/sub/widgets, prefix: wid}\n  - glab:git.example.com/acme/infra\n"+
		"people:\n  - {name: ann, tracker: Ann Lee, host: annl, emails: [ann@acme.example]}\n---\n")

	cfg, _, err := readBoardConfig()
	if err != nil {
		t.Fatal(err)
	}

	var got []string

	for _, r := range cfg.Repos {
		ref, err := parseRepo(r.Repo, r.Prefix)
		if err != nil {
			t.Fatal(err)
		}

		got = append(got, ref.CLI+" "+ref.id()+" "+ref.Prefix+" "+ref.repo())
	}

	want := "gh github.com/acme/widgets WID acme/widgets|glab gitlab.com/acme/sub/widgets WID acme/sub/widgets|" +
		"glab git.example.com/acme/infra INF git.example.com/acme/infra"
	if strings.Join(got, "|") != want {
		t.Errorf("repos = %q, want %q", strings.Join(got, "|"), want)
	}

	if len(cfg.People) != 1 || cfg.People[0].Host != "annl" || len(cfg.People[0].Emails) != 1 {
		t.Errorf("people = %+v", cfg.People)
	}

	writeUserFile(t, home, boardConfigFile, "---\npeople:\n  - {host: annl}\n---\n")

	if _, _, err := readBoardConfig(); err == nil || !strings.Contains(err.Error(), "no name") {
		t.Errorf("err = %v, want the nameless person refused", err)
	}
}

// A watched repository runs through the whole scope: --all adds it, the
// host is asked with the repository named, and one a checkout in the
// scope already covers is dropped. The host CLI is scripted; git is real.
func TestBoardAllReadsWatchedRepos(t *testing.T) {
	home := isolateEnv(t)

	root := canonical(t, t.TempDir())
	good := scopeRepo(t, root, "good")
	git(t, good, "remote", "add", "origin", "https://github.com/acme/good.git")

	writeUserFile(t, home, boardConfigFile, "---\nrepos: [github.com/acme/good, github.com/acme/other]\n---\n")
	t.Setenv(projects.RootsEnv, filepath.Join(root, "*"))

	prev := shell.Capture
	t.Cleanup(func() { shell.Capture = prev })

	var (
		mu    sync.Mutex // projects are collected concurrently
		calls []string
	)

	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
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
	}

	out, err := runStandupApp(t, "board", "--all", "--json", "--state")
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

	out, err = runStandupApp(t, "board", "--all", "--json")
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
	writeUserFile(t, home, boardConfigFile, "---\nrepos: [git.example.com/acme/x]\n---\n")

	if _, err := runStandupApp(t, "-C", good, "board", "--json"); err != nil {
		t.Errorf("one checkout with a bad repos entry: %v, want it unread", err)
	}

	if _, err := runStandupApp(t, "board", "--all"); err == nil || !strings.Contains(err.Error(), "repos") {
		t.Errorf("--all with a bad repos entry: %v, want it named", err)
	}
}

// Rows of several projects group under the person each resolves to, the
// unresolved last, and each line names its project.
func TestRenderByPerson(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	boards := []lookaround.Board{
		{Project: lookaround.Project{Name: "api"}, Rows: []lookaround.Row{
			{Key: "FT-1", Stage: lookaround.StageNext, Who: lookaround.Who{Person: "bob"}},
			{Key: "FT-2", Stage: lookaround.StageInReview, Who: lookaround.Who{Person: "ann"}, LastActivity: at},
			{Key: "#9", Stage: lookaround.StagePROpen},
		}},
		{Project: lookaround.Project{Name: "web"}, Rows: []lookaround.Row{
			{Key: "WEB-3", Stage: lookaround.StageDone, Who: lookaround.Who{Person: "ann"}, LastActivity: at},
		}},
	}

	var out bytes.Buffer

	renderByPerson(&out, boards, 0)

	var heads, lines []string

	for l := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}

		if !strings.Contains(l, "  ") {
			heads = append(heads, l)
		} else {
			lines = append(lines, strings.Join(strings.Fields(l)[:2], " "))
		}
	}

	if got, want := strings.Join(heads, ","), "ann,bob,nobody"; got != want {
		t.Errorf("headings = %q, want %q\n%s", got, want, out.String())
	}

	if got, want := strings.Join(lines, ","), "api FT-2,web WEB-3,api FT-1,api #9"; got != want {
		t.Errorf("rows = %q, want %q\n%s", got, want, out.String())
	}
}

// The glance counts what is in flight and what waits on a person, names
// the oldest, and says failed once for a project nothing could read.
func TestRenderSummary(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	boards := []lookaround.Board{
		{
			Project: lookaround.Project{Name: "api"},
			Sources: []lookaround.Source{{Name: "host", OK: true}},
			Rows: []lookaround.Row{
				{Key: "FT-1", Stage: lookaround.StageNext, Next: lookaround.Next{Command: "f10 start FT-1"}},
				{
					Key: "FT-2", Stage: lookaround.StageInReview, LastActivity: now.Add(-50 * time.Hour),
					Next: lookaround.Next{Command: "/f10:resolve FT-2"},
				},
				{
					Key: "FT-3", Stage: lookaround.StagePROpen, LastActivity: now.Add(-3 * time.Hour),
					Next: lookaround.Next{Text: "waiting on review"},
				},
				{Key: "FT-4", Stage: lookaround.StageDone},
			},
		},
		lookaround.Unreachable(
			lookaround.Project{Name: "web"}, os.ErrNotExist, now, lookaround.Window{Since: now}, lookaround.ViewState,
		),
		{Project: lookaround.Project{Name: "ops"}, Sources: []lookaround.Source{
			{Name: "host", Error: "gh: down"}, {Name: "tracker", OK: true}, {Name: "local", OK: true},
		}},
	}

	var out bytes.Buffer

	renderSummary(&out, boards, now, 0)

	for _, want := range []string{
		"api  2 in flight  1 waiting on you  oldest FT-2 2d",
		"web  failed  file does not exist",
		"ops  nothing in flight  host failed  gh: down",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, out.String())
		}
	}
}

func TestTitleAfterKey(t *testing.T) {
	for _, tc := range []struct{ title, key, want string }{
		{"FT-121: Show f10 board", "FT-121", "Show f10 board"},
		{"FT-121 - Show f10 board", "FT-121", "Show f10 board"},
		{"FT-121: Show f10 board", "FT-12", "FT-121: Show f10 board"},
		{"Show f10 board", "FT-121", "Show f10 board"},
		{"FT-121", "FT-121", "FT-121"},
		{"#12 title", "", "#12 title"},
	} {
		if got := titleAfterKey(tc.title, tc.key); got != tc.want {
			t.Errorf("titleAfterKey(%q, %q) = %q, want %q", tc.title, tc.key, got, tc.want)
		}
	}
}
