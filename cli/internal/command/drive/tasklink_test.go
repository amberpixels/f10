package drive_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	askscmd "github.com/amberpixels/f10/cli/internal/command/asks"
	boardcmd "github.com/amberpixels/f10/cli/internal/command/board"
	standupcmd "github.com/amberpixels/f10/cli/internal/command/standup"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/tasktree"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

var commandLine = regexp.MustCompile(`(?m)^\s*f10 .*$`)

// requireLinked fails on any of ids printed outside a link to its own url,
// and on an id that is not printed at all.
func requireLinked(t *testing.T, out string, ids ...string) {
	t.Helper()

	for _, m := range testkit.LinkSpan.FindAllStringSubmatch(out, -1) {
		if strings.HasPrefix(m[2], "FT-") && m[1] != testkit.IssueURL(m[2]) {
			t.Errorf("%s links to %s", m[2], m[1])
		}
	}

	// a command line to copy (f10 drive FT-1 FT-2) keeps its ids plain
	bare := commandLine.ReplaceAllString(testkit.LinkSpan.ReplaceAllString(out, ""), "")

	for _, id := range ids {
		if !strings.Contains(out, id) {
			t.Errorf("%s is not printed:\n%q", id, out)
		}

		if regexp.MustCompile(regexp.QuoteMeta(id) + `\b`).MatchString(bare) {
			t.Errorf("%s is printed plain:\n%q", id, out)
		}
	}
}

// Every renderer that prints task ids for people prints them as links on a
// terminal, and prints no escape code anywhere else. A new renderer of task
// ids gets a row here.
func TestTaskIDsPrintThroughLinks(t *testing.T) {
	type renderer func(t *testing.T, on bool) string

	cases := map[string]renderer{
		"drive list": func(t *testing.T, _ bool) string {
			root := t.TempDir()
			if err := drivestore.Create(root, &drivestore.Record{
				Args:  []string{"FT-1", "FT-2"},
				Tasks: []drivestore.Task{{ID: "FT-1"}, {ID: "FT-2"}},
			}); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if err := drive.DriveList(
				t.Context(),
				&out,
				render.LinksFor(&out, false),
				testkit.LinkProject,
				root,
				false,
				nil,
			); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"daemon status": func(t *testing.T, _ bool) string {
			root := t.TempDir()
			if err := drivestore.Create(
				root,
				&drivestore.Record{Tasks: []drivestore.Task{{ID: "FT-1"}, {ID: "FT-2"}}},
			); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			drive.WriteDaemonStatus(t.Context(), &out, render.LinksFor(&out, false),
				daemon.Reply{Runs: []daemon.Run{{Root: root, Dir: "/x", Drive: 1}}})

			return out.String()
		},
		"asks list": func(t *testing.T, _ bool) string {
			dir := t.TempDir()
			a := testkit.Raised(t, dir, asks.Ask{Task: "FT-1", URL: testkit.IssueURL("FT-1"), Root: "/r", Project: "r"})
			testkit.Raised(t, dir, asks.Ask{Task: "FT-2", URL: testkit.IssueURL("FT-2"), Root: "/r"})

			if _, err := asks.Claim(dir, a.Key(), asks.ByPane, "wait", true, time.Now()); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if err := askscmd.ListAsks(t.Context(), &out, dir, time.Now(), false); err != nil {
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

			links := render.LinksFor(&out, false)
			standupcmd.LoadStandupLinks(t.Context(), links, rep)

			if err := standupcmd.RenderStandup(&out, links, rep); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"start and finish report": func(t *testing.T, _ bool) string {
			var out bytes.Buffer
			if err := render.WriteReport(
				&out,
				[]render.Fact{render.TaskFact(t.Context(), &out, "/x", "FT-1+FT-2")},
				nil,
			); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"drive report": func(t *testing.T, _ bool) string {
			var out bytes.Buffer

			in := &drive.DriveInput{
				Out:     &out,
				Answers: drive.NewAnswerBook(),
			}
			in.LoadLinks(t.Context(), []drive.DriveTask{{Ref: ref.Ref{ID: "FT-1"}}, {Ref: ref.Ref{ID: "FT-2"}}})

			runs := []*drive.DriveRun{
				{Task: drive.DriveTask{Ref: ref.Ref{ID: "FT-1"}}, Phase: drive.AtHeld},
				{Task: drive.DriveTask{Ref: ref.Ref{ID: "FT-2"}}},
			}
			if err := in.Report(runs, nil); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"drive progress": func(t *testing.T, _ bool) string {
			var out bytes.Buffer

			in := &drive.DriveInput{Out: &out}
			in.LoadLinks(t.Context(), []drive.DriveTask{{Ref: ref.Ref{ID: "FT-1"}}, {Ref: ref.Ref{ID: "FT-2"}}})
			in.Queue(&drive.DriveRun{Task: drive.DriveTask{Ref: ref.Ref{ID: "FT-1"}}})
			in.Queue(&drive.DriveRun{Task: drive.DriveTask{Ref: ref.Ref{ID: "FT-2"}}})

			return out.String()
		},
		"status": func(t *testing.T, on bool) string {
			var out bytes.Buffer

			rows := []statusview.TaskRow{
				{ID: "FT-1", URL: testkit.IssueURL("FT-1"), Stage: statusview.StageDone},
				{ID: "FT-2", URL: testkit.IssueURL("FT-2")},
			}
			if err := statusview.RenderRows(&out, rows, statusview.RowView{Term: on, Plain: !on}); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
		"board": func(t *testing.T, on bool) string {
			var out bytes.Buffer

			lines := []boardcmd.BoardLine{
				{Key: "FT-1", URL: testkit.IssueURL("FT-1")},
				{Key: "FT-2", URL: testkit.IssueURL("FT-2")},
			}

			limit := 0
			if on {
				limit = 120
			}

			links := render.LinksOn(limit > 0)

			nodes := make([]tasktree.Node, len(lines))
			for i, l := range lines {
				nodes[i] = boardcmd.BoardNode(l, lookaround.Project{}, false, links)
			}

			if err := (tasktree.Tree{Align: true, Width: limit}).Write(&out, nodes); err != nil {
				t.Fatal(err)
			}

			return out.String()
		},
	}

	for name, r := range cases {
		t.Run(name+"/terminal", func(t *testing.T) {
			testkit.WithLinks(t, true)
			requireLinked(t, r(t, true), "FT-1", "FT-2")
		})

		t.Run(name+"/piped", func(t *testing.T) {
			testkit.WithLinks(t, false)

			if out := r(t, false); strings.Contains(out, "\x1b]8;") {
				t.Errorf("piped output links:\n%q", out)
			}
		})
	}
}
