package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A refusal case: the verb's argv, what stdin holds (nil: stdin must not be
// read), the exit code (-1: a plain error) and text the error carries.
type refusal struct {
	args  []string
	stdin io.Reader
	exit  int
	want  []string
	out   string // stdout of a verb that answers instead of failing
}

// Every task verb on a tracker f10 cannot reach: reads fail plainly, writes
// exit 3 (a missing host CLI stays a plain error, as host writes always
// were), and the write verbs with nothing to try refuse before stdin.
func TestUnreachableTrackerRefusals(t *testing.T) {
	const (
		noDriver = "no driver was found (looked for"
		spec     = "see docs/driver-contract.md"
		adapter  = "create it through the project's Create adapter (project.md, Tracker)"
		nothing  = "nothing was written"
		noCLI    = "no host CLI for this checkout: no origin remote"
	)

	setups := []struct {
		name  string
		repo  func(t *testing.T) string
		cases []refusal
	}{
		{
			"a Notion tracker with no driver",
			func(t *testing.T) string { return createRepo(t, "") },
			[]refusal{
				{args: []string{"read", "FT-5"}, exit: -1, want: []string{"Notion", noDriver, spec, "fetch adapter"}},
				{args: []string{"open", "FT-5", "--link"}, exit: -1, want: []string{noDriver, spec}},
				{args: []string{"open", "--link"}, exit: -1, want: []string{noDriver, spec}},
				{args: []string{"open", "--print"}, exit: -1, want: []string{noDriver, spec}},
				{args: []string{"search", "x"}, exit: -1, want: []string{noDriver, spec}},
				{args: []string{"children", "FT-5"}, exit: -1, want: []string{noDriver, spec}},
				{args: []string{"comment", "FT-5"}, exit: ui.ExitDeclined, want: []string{noDriver, nothing}},
				{
					args: []string{"update", "FT-5", "--status", "done"},
					exit: ui.ExitDeclined,
					want: []string{noDriver, nothing},
				},
				{
					args: []string{"update", "FT-5", "--parent", "FT-6"},
					exit: ui.ExitDeclined,
					want: []string{noDriver, nothing},
				},
				{args: []string{"create"}, exit: ui.ExitDeclined, want: []string{noDriver, adapter}},
				{args: []string{"stale", "FT-5"}, out: "undated\n"},
				{args: []string{"ref", "FT-5"}, out: "FT-5\n"},
			},
		},
		{
			"a Notion tracker whose driver declines",
			func(t *testing.T) string { return createRepo(t, "exit 3\n") },
			[]refusal{
				{
					args: []string{"read", "FT-5"},
					exit: -1,
					want: []string{"declines `read`, which `f10 task read`", spec},
				},
				{args: []string{"open", "FT-5", "--link"}, exit: -1, want: []string{"declines `url`", spec}},
				{args: []string{"open", "--link"}, exit: -1, want: []string{"declines `index`", spec}},
				{args: []string{"open", "--print"}, exit: -1, want: []string{"declines `list`", spec}},
				{args: []string{"search", "x"}, exit: -1, want: []string{"declines `search`", spec}},
				{args: []string{"children", "FT-5"}, exit: ui.ExitDeclined, want: []string{"no parent link"}},
				{
					args: []string{"comment", "FT-5"}, stdin: strings.NewReader("hi"), exit: ui.ExitDeclined,
					want: []string{"declines `comment`", nothing},
				},
				{
					args: []string{"update", "FT-5", "--status", "done"},
					exit: ui.ExitDeclined,
					want: []string{"declines `update`", nothing},
				},
				{
					args: []string{"update", "FT-5", "--parent", "FT-6"}, exit: ui.ExitDeclined,
					want: []string{"declines `update --parent`", nothing},
				},
				{
					args: []string{"create"}, stdin: strings.NewReader("# A\n"), exit: ui.ExitDeclined,
					want: []string{"declines `create`", adapter},
				},
				{args: []string{"stale", "FT-5"}, out: "undated\n"},
				{args: []string{"ref", "FT-5"}, out: "FT-5\n"},
			},
		},
		{"a GitHub tracker with no host CLI", noCLIRepo, []refusal{
			{args: []string{"read", "FT-5"}, exit: -1, want: []string{noCLI, "fetch adapter"}},
			{args: []string{"open", "FT-5", "--link"}, exit: -1, want: []string{noCLI}},
			{args: []string{"open", "--link"}, exit: -1, want: []string{noCLI}},
			{args: []string{"open", "--print"}, exit: -1, want: []string{noCLI}},
			{args: []string{"search", "x"}, exit: -1, want: []string{noCLI}},
			{args: []string{"children", "FT-5"}, exit: -1, want: []string{noCLI}},
			{args: []string{"comment", "FT-5"}, exit: -1, want: []string{noCLI, nothing}},
			{args: []string{"update", "FT-5", "--status", "done"}, exit: -1, want: []string{noCLI, nothing}},
			{args: []string{"update", "FT-5", "--parent", "FT-6"}, exit: -1, want: []string{noCLI, nothing}},
			{args: []string{"create"}, exit: ui.ExitDeclined, want: []string{noCLI, adapter}},
			{args: []string{"stale", "FT-5"}, exit: -1, want: []string{noCLI}},
			{args: []string{"ref", "FT-5"}, out: "FT-5\n"},
		}},
	}

	for _, s := range setups {
		repo := s.repo(t)
		withStaleAfter(t, repo)

		for _, c := range s.cases {
			t.Run(s.name+"/"+strings.Join(c.args, " "), func(t *testing.T) {
				stdin := c.stdin
				if stdin == nil {
					stdin = failingReader{t}
				}

				r := runAppStdin(t, stdin, append([]string{"-C", repo, "task"}, c.args...)...)

				if c.want == nil {
					if r.err != nil || r.out != c.out {
						t.Errorf("got %q, %v; want %q", r.out, r.err, c.out)
					}

					return
				}

				if r.err == nil || exitCode(r.err) != c.exit {
					t.Fatalf("err = %v (exit %d), want exit %d", r.err, exitCode(r.err), c.exit)
				}

				for _, w := range c.want {
					if !strings.Contains(r.err.Error(), w) {
						t.Errorf("err = %v, want it to carry %q", r.err, w)
					}
				}
			})
		}
	}
}

// noCLIRepo is a GitHub tracker with no origin, so no host CLI is settled.
func noCLIRepo(t *testing.T) string {
	t.Helper()

	repo, _ := hostRepo(t, "")
	if out, err := exec.Command("git", "-C", repo, "remote", "remove", "origin").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %v %s", err, out)
	}

	return repo
}

// withStaleAfter declares stale_after, so a bare task stale asks the tracker.
func withStaleAfter(t *testing.T, repo string) {
	t.Helper()

	path := repo + "/.f10/instructions/project.md"
	md := strings.Replace(readFile(t, path), "tracker:", "stale_after: 3d\ntracker:", 1)
	writeFile(t, path, md)
}

// tasks picks the source from the driver, the tracker kind and the host CLI.
func TestTasksPicksTheSource(t *testing.T) {
	for _, tc := range []struct {
		driver bool
		kind   facts.TrackerKind
		cli    string
		want   string
	}{
		{false, facts.TrackerGitHub, "gh", "main.hostSource"},
		{false, facts.TrackerGitHub, "", "main.refusedSource"},
		{false, facts.TrackerNotion, "gh", "main.refusedSource"},
		{true, facts.TrackerGitHub, "gh", "main.fallbackSource main.hostSource"},
		{true, facts.TrackerGitHub, "", "main.fallbackSource main.refusedSource"},
		{true, facts.TrackerNotion, "gh", "main.fallbackSource main.refusedSource"},
	} {
		name := fmt.Sprintf("driver %v, %s, cli %q", tc.driver, tc.kind, tc.cli)
		t.Run(name, func(t *testing.T) {
			tg := declaredTracker(t, tc.kind)
			tg.Probes = &probe.Probes{CLI: probe.Signal{Value: tc.cli, Evidence: "none"}}

			if tc.driver {
				writeDriver(t, tg.Layout.StorageRoot.Path())
			}

			src := tasksFor(tg)
			got := fmt.Sprintf("%T", src)

			if f, ok := src.(fallbackSource); ok {
				got += fmt.Sprintf(" %T", f.rest)
			}

			if got != tc.want {
				t.Errorf("tasks() = %s, want %s", got, tc.want)
			}
		})
	}
}

// Only the driver's exit 3 reaches the host: any other driver failure is
// the answer, and the host is never asked.
func TestFallbackOnlyOnUnsupported(t *testing.T) {
	f := newFakes(t)
	tg := declaredTracker(t, facts.TrackerGitHub)
	writeDriver(t, tg.Layout.StorageRoot.Path())

	f.fail("driver list", shell.Result{Code: 1, Stderr: "tracker down"})

	if _, err := taskRows(t.Context(), tg, issuePage); err == nil || !strings.Contains(err.Error(), "tracker down") {
		t.Errorf("err = %v, want the driver's failure", err)
	}

	if f.called("gh issue list") {
		t.Error("a failing driver fell back to host issues")
	}

	f.fail("driver search x", shell.Result{Code: driver.ExitUnsupported})
	f.script("gh issue list --search x --json number,title,state,url,labels,body,assignees,createdAt --limit 30", `[]`)

	if _, err := searchRows(t.Context(), tg, "x"); err != nil || !f.called("gh issue list") {
		t.Errorf("err = %v; want the declined search on host issues", err)
	}
}
