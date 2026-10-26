package forge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// script answers each command line, name and args joined, from a map.
func script(t *testing.T, answers map[string]string) {
	t.Helper()

	prev := shell.Capture

	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		key := name + " " + strings.Join(args, " ")

		out, ok := answers[key]
		if !ok {
			t.Fatalf("unscripted call: %q", key)
		}

		if rest, failed := strings.CutPrefix(out, "FAIL "); failed {
			return shell.Result{Code: 1, Stderr: rest}, nil
		}

		return shell.Result{Stdout: out}, nil
	}

	t.Cleanup(func() { shell.Capture = prev })
}

func TestStatus(t *testing.T) {
	for in, want := range map[string]string{
		"OPEN": Open, "opened": Open, "locked": Open, "MERGED": Merged, "closed": Closed, "": "",
	} {
		if got := Status(in); got != want {
			t.Errorf("Status(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameLogin(t *testing.T) {
	for _, c := range []struct {
		declared, login string
		want            bool
	}{
		{"claude", "Claude", true}, {"claude", "claude[bot]", true}, {"claude[bot]", "claude", false}, {"", "x", false},
	} {
		if got := SameLogin(c.declared, c.login); got != c.want {
			t.Errorf("SameLogin(%q, %q) = %v", c.declared, c.login, got)
		}
	}
}

// glab prints one document per page; every page is read.
func TestDecodePagesReadsEveryDocument(t *testing.T) {
	script(t, map[string]string{"glab api --paginate x": "[1,2]\n[3]\n"})

	got, err := DecodePages[int](t.Context(), Host{Name: "glab"}, "x")
	if err != nil || len(got) != 3 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestPullCommitsKeepsTheMessage(t *testing.T) {
	script(t, map[string]string{
		"gh api --paginate repos/{owner}/{repo}/pulls/7/commits?per_page=100": `[{"sha":"a1",
		  "commit":{"message":"Guard the list\n\nTask: GH-12\nStep: implement\n","author":{"date":"2026-10-14T09:00:00Z"}}}]`,
	})

	got, err := Host{Name: "gh"}.PullCommits(t.Context(), 7)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}

	if got[0].Subject != "Guard the list" || !strings.Contains(got[0].Message, "Task: GH-12") {
		t.Errorf("commit = %+v", got[0])
	}
}

func TestPullReadsTheBoardFields(t *testing.T) {
	p := pull{
		Number: 3, State: "OPEN", HeadRefName: "FT-1/x", Mergeable: "MERGEABLE", MergeStateStatus: "BLOCKED",
		ReviewDecision: "CHANGES_REQUESTED",
	}
	p.StatusCheckRollup = append(p.StatusCheckRollup,
		struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		}{Status: "COMPLETED", Conclusion: "SUCCESS"},
		struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		}{Status: "IN_PROGRESS"},
	)

	got := p.pr()
	if got.Status != Open || got.Mergeable != Blocked || got.Decision != ChangesRequested || got.Checks != Pending {
		t.Errorf("pr = %+v", got)
	}

	p.StatusCheckRollup[1].Status, p.StatusCheckRollup[1].Conclusion = "COMPLETED", "FAILURE"
	if got := p.pr(); got.Checks != Red {
		t.Errorf("checks = %q, want red", got.Checks)
	}

	gl := pull{IID: 4, State: "merged", DetailedMergeStatus: "conflict", MergedAtGL: time.Now()}
	gl.Pipeline = &struct {
		Status string `json:"status"`
	}{Status: "success"}

	if got := gl.pr(); got.Number != 4 || got.Status != Merged || got.Mergeable != Conflicts || got.Checks != Green {
		t.Errorf("mr = %+v", got)
	}
}

// PullsSince is open requests unbounded and merged ones since the window.
func TestPullsSince(t *testing.T) {
	since := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	list := func(q string) string {
		return "gh pr list --state all --search " + q + " --json " + BoardFields + " --limit 1000"
	}

	script(t, map[string]string{
		list("is:open"): `[{"number":5,"state":"OPEN","headRefName":"FT-5/x"}]`,
		list("is:merged merged:>=2026-10-16T00:00:00Z"): `[{"number":4,"state":"MERGED","mergedAt":"2026-10-20T00:00:00Z"}]`,
	})

	got, err := Host{Name: "gh"}.PullsSince(t.Context(), since)
	if err != nil || len(got) != 2 || got[0].Number != 5 || got[1].Status != Merged {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// A host naming its repository outright carries it on every command: -R
// on a noun command, the placeholders filled on an api call, and nothing
// at all once Repo is empty.
func TestRunNamesTheRepository(t *testing.T) {
	var got []string

	prev := shell.Capture
	shell.Capture = func(_ context.Context, _, name string, args ...string) (shell.Result, error) {
		got = append(got, name+" "+strings.Join(args, " "))

		return shell.Result{Stdout: "[]"}, nil
	}

	t.Cleanup(func() { shell.Capture = prev })

	ctx := context.Background()

	for _, c := range []struct {
		host Host
		args []string
		want string
	}{
		{
			Host{Name: "gh", Repo: "acme/widgets"},
			[]string{"pr", "list", "--search", "is:open"},
			"gh pr list --search is:open -R acme/widgets",
		},
		{
			Host{Name: "gh", Repo: "acme/widgets"},
			[]string{"api", "repos/{owner}/{repo}/branches/x"},
			"gh api repos/acme/widgets/branches/x",
		},
		{
			Host{Name: "gh", Repo: "acme/widgets"},
			[]string{"api", "graphql", "-F", "owner={owner}", "-F", "name={repo}"},
			"gh api graphql -F owner=acme -F name=widgets",
		},
		{
			Host{Name: "gh", Repo: "ghe.example.com/acme/widgets"},
			[]string{"issue", "list"},
			"gh issue list -R ghe.example.com/acme/widgets",
		},
		{
			Host{Name: "gh", Repo: "ghe.example.com/acme/widgets"},
			[]string{"api", "repos/{owner}/{repo}/branches/x"},
			"gh api repos/acme/widgets/branches/x --hostname ghe.example.com",
		},
		{
			Host{Name: "glab", Repo: "acme/sub/widgets"},
			[]string{"api", "projects/:fullpath/merge_requests?state=opened"},
			"glab api projects/acme%2Fsub%2Fwidgets/merge_requests?state=opened",
		},
		{
			Host{Name: "glab", Repo: "git.example.com/acme/widgets"},
			[]string{"mr", "list"},
			"glab mr list -R https://git.example.com/acme/widgets",
		},
		{
			Host{Name: "gh", Dir: "/code/widgets"},
			[]string{"api", "repos/{owner}/{repo}/branches/x"},
			"gh api repos/{owner}/{repo}/branches/x",
		},
	} {
		got = nil

		if _, err := c.host.Run(ctx, c.args...); err != nil {
			t.Fatal(err)
		}

		if len(got) != 1 || got[0] != c.want {
			t.Errorf("Run(%v) ran %q, want %q", c.args, got, c.want)
		}
	}
}

// Both hosts' commit APIs name the author's email beside the message.
func TestPullCommitsKeepTheEmail(t *testing.T) {
	script(t, map[string]string{
		"gh api --paginate repos/{owner}/{repo}/pulls/7/commits?per_page=100": `[{"sha":"a1","commit":{"message":"x",` +
			`"author":{"email":"ann@acme.example","date":"2026-10-20T10:00:00Z"}}}]`,
		"glab api --paginate projects/:fullpath/merge_requests/7/commits?per_page=100": `[{"id":"b2","title":"y",` +
			`"message":"y","author_email":"bob@acme.example","authored_date":"2026-10-20T10:00:00Z"}]`,
	})

	for _, c := range []struct{ name, email string }{{"gh", "ann@acme.example"}, {"glab", "bob@acme.example"}} {
		commits, err := Host{Name: c.name, Dir: "/code/widgets"}.PullCommits(context.Background(), 7)
		if err != nil || len(commits) != 1 || commits[0].Email != c.email {
			t.Errorf("%s: commits = %+v, %v, want one by %s", c.name, commits, err, c.email)
		}
	}
}
