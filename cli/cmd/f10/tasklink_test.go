package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/standup"
)

const linkProject = "github.com/o/r"

func issueURL(id string) string {
	return "https://github.com/o/r/issues/" + strings.TrimPrefix(id, "FT-")
}

// withLinks seeds the ledger with FT-1..FT-3 and makes every writer a
// terminal when on; projectOf answers linkProject for any directory.
func withLinks(t *testing.T, on bool) {
	t.Helper()

	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	prevTerm, prevProject := linkTerminal, projectOf
	t.Cleanup(func() { linkTerminal, projectOf = prevTerm, prevProject })

	linkTerminal = func(io.Writer) bool { return on }
	projectOf = func(context.Context, string) string { return linkProject }

	ledger.RememberRefs(t.Context(), linkProject,
		ledger.TaskRef{ID: "FT-1", Title: "One", URL: issueURL("FT-1")},
		ledger.TaskRef{ID: "FT-2", Title: "Two", URL: issueURL("FT-2")},
		ledger.TaskRef{ID: "FT-3", Title: "Three", URL: issueURL("FT-3")},
	)
}

var linkSpan = regexp.MustCompile("\x1b\\]8;;([^\x1b]*)\x1b\\\\([^\x1b]*)\x1b\\]8;;\x1b\\\\")

var commandLine = regexp.MustCompile(`(?m)^\s*f10 .*$`)

// requireLinked fails on any of ids printed outside a link to its own url,
// and on an id that is not printed at all.
func requireLinked(t *testing.T, out string, ids ...string) {
	t.Helper()

	for _, m := range linkSpan.FindAllStringSubmatch(out, -1) {
		if strings.HasPrefix(m[2], "FT-") && m[1] != issueURL(m[2]) {
			t.Errorf("%s links to %s", m[2], m[1])
		}
	}

	// a command line to copy (f10 drive FT-1 FT-2) keeps its ids plain
	bare := commandLine.ReplaceAllString(linkSpan.ReplaceAllString(out, ""), "")

	for _, id := range ids {
		if !strings.Contains(out, id) {
			t.Errorf("%s is not printed:\n%q", id, out)
		}

		if regexp.MustCompile(regexp.QuoteMeta(id) + `\b`).MatchString(bare) {
			t.Errorf("%s is printed plain:\n%q", id, out)
		}
	}
}

func TestTaskLinksID(t *testing.T) {
	withLinks(t, true)

	l := linksFor(io.Discard, false)
	l.Load(t.Context(), linkProject, "FT-1+FT-2", "FT-9")

	if got := l.ID(linkProject, "FT-9"); got != "FT-9" {
		t.Errorf("an id the ledger lacks = %q", got)
	}

	if got, want := l.ID(linkProject, "FT-1+FT-2"),
		hyperlink(issueURL("FT-1"), "FT-1")+"+"+hyperlink(issueURL("FT-2"), "FT-2"); got != want {
		t.Errorf("bundle = %q, want %q", got, want)
	}

	if got := l.ID("other", "FT-1"); got != "FT-1" {
		t.Errorf("an id under another project = %q", got)
	}

	if got := linksFor(io.Discard, true); got.on {
		t.Error("--plain links")
	}

	var none *taskLinks
	if none.ID(linkProject, "FT-1") != "FT-1" {
		t.Error("a nil taskLinks links")
	}
}

// Off, Load never opens the ledger, so a command piped to a file makes no
// database read.
func TestTaskLinksOffReadsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f10.db")
	t.Setenv(ledger.PathEnv, path)

	linksOn(false).Load(t.Context(), linkProject, "FT-1")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the ledger was touched: %v", err)
	}
}

func TestPadRightCountsVisibleText(t *testing.T) {
	if got := padRight(hyperlink("https://x", "FT-1"), 6); visibleWidth(got) != 6 || !strings.HasSuffix(got, "  ") {
		t.Errorf("padRight = %q", got)
	}
}

// Every renderer that prints task ids for people prints them as links on a
// terminal, and prints no escape code anywhere else. A new renderer of task
// ids gets a row here.
func TestTaskIDsPrintThroughLinks(t *testing.T) {
	type render func(t *testing.T, on bool) string

	cases := map[string]render{
		"drive list": func(t *testing.T, _ bool) string {
			root := t.TempDir()
			if err := drives.Create(root, &drives.Record{
				Args:  []string{"FT-1", "FT-2"},
				Tasks: []drives.Task{{ID: "FT-1"}, {ID: "FT-2"}},
			}); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if err := driveList(t.Context(), &out, linksFor(&out, false), linkProject, root, false, nil); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"daemon status": func(t *testing.T, _ bool) string {
			root := t.TempDir()
			if err := drives.Create(
				root,
				&drives.Record{Tasks: []drives.Task{{ID: "FT-1"}, {ID: "FT-2"}}},
			); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			writeDaemonStatus(t.Context(), &out, linksFor(&out, false),
				daemon.Reply{Runs: []daemon.Run{{Root: root, Dir: "/x", Drive: 1}}})

			return out.String()
		},
		"asks list": func(t *testing.T, _ bool) string {
			dir := t.TempDir()
			a := raised(t, dir, asks.Ask{Task: "FT-1", URL: issueURL("FT-1"), Root: "/r", Project: "r"})
			raised(t, dir, asks.Ask{Task: "FT-2", URL: issueURL("FT-2"), Root: "/r"})

			if _, err := asks.Claim(dir, a.Key(), asks.ByPane, "wait", true, time.Now()); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if err := listAsks(&out, dir, time.Now(), false); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"standup": func(t *testing.T, _ bool) string {
			rep := standup.Standup{Projects: []standup.Project{{
				Name:  "r",
				Repos: []standup.Repo{{Name: "r", Checkout: "/x"}},
				Tasks: []standup.Task{{ID: "FT-1", State: "merged"}, {ID: "FT-2", Class: standup.ClassUpkeep}},
			}}}

			var out bytes.Buffer

			links := linksFor(&out, false)
			loadStandupLinks(t.Context(), links, rep)

			if err := renderStandup(&out, links, rep); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"start and finish report": func(t *testing.T, _ bool) string {
			var out bytes.Buffer
			if err := writeReport(&out, []fact{taskFact(t.Context(), &out, "/x", "FT-1+FT-2")}, nil); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"drive report": func(t *testing.T, _ bool) string {
			var out bytes.Buffer

			in := &driveInput{answers: map[string]string{}}
			in.loadLinks(t.Context(), &out, []driveTask{{ref: ref.Ref{ID: "FT-1"}}, {ref: ref.Ref{ID: "FT-2"}}})

			runs := []*driveRun{
				{task: driveTask{ref: ref.Ref{ID: "FT-1"}}, queued: true},
				{task: driveTask{ref: ref.Ref{ID: "FT-2"}}},
			}
			if err := in.report(&out, runs, nil); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"status": func(t *testing.T, on bool) string {
			var out bytes.Buffer

			rows := []taskRow{
				{ID: "FT-1", URL: issueURL("FT-1"), Stage: stageDone},
				{ID: "FT-2", URL: issueURL("FT-2")},
			}
			if err := renderRows(&out, rows, rowView{term: on, plain: !on}); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"board": func(t *testing.T, on bool) string {
			var out bytes.Buffer

			lines := []boardLine{{Key: "FT-1", URL: issueURL("FT-1")}, {Key: "FT-2", URL: issueURL("FT-2")}}

			limit := 0
			if on {
				limit = 120
			}

			lay := newBoardLayout(lines, nil, limit)
			for _, l := range lines {
				lay.line(&out, l, "")
			}

			return out.String()
		},
	}

	for name, r := range cases {
		t.Run(name+"/terminal", func(t *testing.T) {
			withLinks(t, true)
			requireLinked(t, r(t, true), "FT-1", "FT-2")
		})

		t.Run(name+"/piped", func(t *testing.T) {
			withLinks(t, false)

			if out := r(t, false); strings.Contains(out, "\x1b]8;") {
				t.Errorf("piped output links:\n%q", out)
			}
		})
	}
}
