package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/lookaround"
)

// The HTML report checked on the recorded boards: the JSON it embeds is
// the board it was given, the page reaches for nothing outside itself,
// and its renderer reduces the board to the same lines the terminal draws.
// The last check runs the page's script under node and skips without it.

// boardDataOpen opens the page's data tag; the JSON sits between it and
// the next closing script tag.
const boardDataOpen = `<script type="application/json" id="board">`

// htmlBoards is every recorded board, plus one with nothing to show and
// one with the shapes the recorded ones lack.
func htmlBoards(t *testing.T) map[string]lookaround.Board {
	t.Helper()

	paths, err := filepath.Glob("testdata/board/*.board.json")
	if err != nil {
		t.Fatal(err)
	}

	boards := map[string]lookaround.Board{}

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		var b lookaround.Board
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		boards[strings.TrimSuffix(filepath.Base(path), ".board.json")] = b
	}

	empty := lookaround.Board{
		Version:     lookaround.Version,
		GeneratedAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
		Project:     lookaround.Project{ID: "github.com/acme/widgets", Name: "widgets", Host: "gh", Remote: true},
		Window:      lookaround.Window{Since: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)},
		Sources: []lookaround.Source{
			{Name: lookaround.SourceHost, Error: "gh: To get started with GitHub CLI, please run:  gh auth login"},
			{Name: lookaround.SourceTracker, OK: true},
			{Name: lookaround.SourceLocal, Skipped: true},
		},
	}
	boards["empty"] = empty

	// the shapes no recorded board holds: a task with no title beside a
	// request, a request naming no task, an agent at work, an address that
	// is not a web address, a title with a quote, a sentence for next
	edges := empty
	edges.Project.Name = ""
	edges.Rows = []lookaround.Row{
		{
			Key:       "FT-1",
			Task:      &lookaround.Task{ID: "FT-1"},
			Stage:     lookaround.StagePROpen,
			StageFrom: lookaround.FromGuessed,
			Where: lookaround.Where{
				PR: &lookaround.PRRef{
					Number: 11,
					Title:  "the request's title",
					URL:    "https://x.io/pull/11",
					State:  "open",
				},
			},
			Next: lookaround.Next{Command: "/f10:review FT-1"},
		},
		{
			Key:       "#12",
			Stage:     lookaround.StageInReview,
			StageFrom: lookaround.FromGuessed,
			Who:       lookaround.Who{Author: "ann", Reviewers: []string{"bob"}},
			Where: lookaround.Where{
				PR: &lookaround.PRRef{Number: 12, Title: "no task here", URL: "https://x.io/pull/12", State: "open"},
			},
			Next: lookaround.Next{Text: "waiting on review"},
		},
		{
			Key: "FT-3", Task: &lookaround.Task{ID: "FT-3", Title: `say "hi" <b>`, URL: "javascript:alert(1)"},
			Stage: lookaround.StageImplementing, StageFrom: lookaround.FromLocal,
			Who:  lookaround.Who{Agent: "ft-3-abc123", AgentStatus: "working", Assignees: []string{"ann"}},
			Next: lookaround.Next{Text: "at implement"},
		},
	}
	boards["edges"] = edges

	// the feed's shapes the recorded ones lack: a local-only checkout, a
	// span over several days, a task in two parts, an abandoned request, a
	// move owed
	feed := empty
	feed.View = lookaround.ViewFeed
	feed.Project.LocalOnly = true
	feed.Sources = []lookaround.Source{
		{Name: lookaround.SourceHost, OK: true, Skipped: true, Notes: []string{localOnly}},
		{Name: lookaround.SourceTracker, OK: true, Skipped: true},
		{Name: lookaround.SourceLocal, OK: true},
	}
	feed.Window = lookaround.Window{
		Since: time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local), End: time.Date(2026, 10, 26, 0, 0, 0, 0, time.Local),
		Chosen: "last-week",
	}
	feed.Feed = &lookaround.Feed{
		Quiet: lookaround.Quiet{Count: 1},
		Rows: []lookaround.FeedRow{
			{
				Group: lookaround.GroupStarted, Key: "FT-2", From: lookaround.FromHost,
				PR: &lookaround.PRRef{Number: 2, Title: "FT-2: again", URL: "https://x.io/pull/2", State: "open"},
				Related: []lookaround.RelatedPR{
					{Number: 1, URL: "https://x.io/pull/1", State: "open", Relation: lookaround.RelationPart},
				},
				Who:     lookaround.Who{Author: "ann"},
				Owes:    "bob",
				Summary: "opened",
			},
			{
				Group: lookaround.GroupFinished, Key: "#3", From: lookaround.FromHost, Abandoned: true,
				PR:      &lookaround.PRRef{Number: 3, Title: "dropped", State: "closed"},
				Summary: "abandoned",
			},
			{
				Group: lookaround.GroupNewTasks, Key: "FT-4", From: lookaround.FromTracker,
				Task: &lookaround.Task{ID: "FT-4", Title: "new"}, Who: lookaround.Who{Assignees: []string{"cat"}},
				Summary: "created",
			},
		},
	}
	boards["feed-edges"] = feed

	quiet := feed
	quiet.Feed = &lookaround.Feed{Rows: []lookaround.FeedRow{}}
	quiet.Window = lookaround.Window{Since: feed.Window.Since, Chosen: "since 2026-10-19", Fallback: true}
	boards["feed-empty"] = quiet

	return boards
}

func renderPage(t *testing.T, b lookaround.Board) string {
	t.Helper()

	var buf bytes.Buffer
	if err := renderBoardHTML(&buf, b); err != nil {
		t.Fatal(err)
	}

	return buf.String()
}

// embeddedJSON cuts the board out of a rendered page.
func embeddedJSON(t *testing.T, page string) string {
	t.Helper()

	_, rest, ok := strings.Cut(page, boardDataOpen)
	if !ok {
		t.Fatal("the page has no data tag")
	}

	raw, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatal("the data tag never closes")
	}

	return raw
}

func TestBoardHTMLRoundTrip(t *testing.T) {
	for name, b := range htmlBoards(t) {
		t.Run(name, func(t *testing.T) {
			raw := embeddedJSON(t, renderPage(t, b))

			var got lookaround.Board
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatalf("the embedded JSON does not parse: %v", err)
			}

			want, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}

			back, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(want, back) {
				t.Errorf("the board changed through the page:\n%s\n%s", want, back)
			}
		})
	}
}

// A title holding a closing script tag cannot end the data tag early.
func TestBoardHTMLEscapesScriptClose(t *testing.T) {
	b := htmlBoards(t)["empty"]
	b.Rows = []lookaround.Row{{
		Key:   "FT-1",
		Task:  &lookaround.Task{ID: "FT-1", Title: `</script><script>alert(1)</script>`},
		Stage: lookaround.StageNext, StageFrom: lookaround.FromGuessed,
	}}

	page := renderPage(t, b)
	if strings.Count(page, "</script>") != 2 {
		t.Errorf("the title broke out of the data tag:\n%s", embeddedJSON(t, page))
	}
}

// The page opens from disk with nothing fetched: no stylesheet, script,
// font or image from anywhere, and the only addresses are the board's own.
func TestBoardHTMLStandsAlone(t *testing.T) {
	if n := strings.Count(boardHTMLPage, boardPlaceholder); n != 1 {
		t.Fatalf("the placeholder occurs %d times, want 1", n)
	}

	for _, reach := range []string{"<link", " src=", "@import", "url("} {
		if strings.Contains(boardHTMLPage, reach) {
			t.Errorf("the page reaches out with %q", reach)
		}
	}

	for name, b := range htmlBoards(t) {
		page := renderPage(t, b)
		shell := strings.Replace(page, embeddedJSON(t, page), "", 1)

		for _, scheme := range []string{"http://", "https://"} {
			if strings.Contains(shell, scheme) {
				t.Errorf("%s: an address outside the board's JSON: %q", name, scheme)
			}
		}
	}
}

// scriptBody is the renderer, cut out of the page for node to run.
func scriptBody(t *testing.T) string {
	t.Helper()

	_, after, ok := strings.CutLast(boardHTMLPage, "<script>")
	if !ok {
		t.Fatal("the page has no renderer script")
	}

	body, _, ok := strings.Cut(after, "</script>")
	if !ok {
		t.Fatal("the renderer script never closes")
	}

	return body
}

// A rendered is one board through the page's script under node: its
// model, and the markup render() made of it.
type rendered struct {
	Model boardModel `json:"model"`
	HTML  string     `json:"html"`
}

// renderUnderNode runs the page's script on every board and returns what
// it drew; it skips the test where node is absent.
func renderUnderNode(t *testing.T) map[string]rendered {
	t.Helper()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH: the page's renderer is checked only where it is")
	}

	dir := t.TempDir()
	harness := filepath.Join(dir, "render.js")
	script := "const fs = require('fs');\n" +
		"const __board = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));\n" +
		scriptBody(t) + "\n" +
		"const __model = model(__board);\n" +
		"process.stdout.write(JSON.stringify({ model: __model, html: render(__board, __model) }));\n"

	if err := os.WriteFile(harness, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	out := map[string]rendered{}

	for name, b := range htmlBoards(t) {
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}

		input := filepath.Join(dir, name+".json")
		if err := os.WriteFile(input, raw, 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := exec.CommandContext(t.Context(), node, harness, input).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: node: %v\n%s", name, err, got)
		}

		var r rendered
		if err := json.Unmarshal(got, &r); err != nil {
			t.Fatalf("%s: the page's output does not parse: %v\n%s", name, err, got)
		}

		out[name] = r
	}

	return out
}

// The page's model() and the terminal's boardLines agree on every board:
// same notes, same empty sentence, same lines in the same order.
func TestBoardHTMLModelMatchesTerminal(t *testing.T) {
	drawn := renderUnderNode(t)

	for name, b := range htmlBoards(t) {
		t.Run(name, func(t *testing.T) {
			want, got := boardLines(b), drawn[name].Model
			if !reflect.DeepEqual(want, got) {
				wantJSON, _ := json.MarshalIndent(want, "", "  ")
				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				t.Errorf("the page and the terminal disagree\nterminal:\n%s\npage:\n%s", wantJSON, gotJSON)
			}
		})
	}
}

// render() draws every line of the model: each key, stage and origin, and
// each note, appear in the markup.
func TestBoardHTMLRenderDrawsEveryRow(t *testing.T) {
	drawn := renderUnderNode(t)

	for name, r := range drawn {
		t.Run(name, func(t *testing.T) {
			for _, n := range r.Model.Notes {
				if !strings.Contains(r.HTML, `class="`+n.Status+`"`) || !strings.Contains(r.HTML, ">"+n.Name+"<") {
					t.Errorf("source %s %s is not drawn", n.Name, n.Status)
				}
			}

			if r.Model.Empty != "" && !strings.Contains(r.HTML, r.Model.Empty) {
				t.Errorf("the empty sentence is not drawn")
			}

			for _, row := range r.Model.Rows {
				for _, want := range []string{
					">" + row.Key + "<",
					`data-stage="` + row.Stage + `">` + row.Stage + "<",
					`class="from">` + row.StageFrom + "<",
				} {
					if !strings.Contains(r.HTML, want) {
						t.Errorf("%s: %q is not drawn", row.Key, want)
					}
				}
			}
		})
	}
}

// What render() escapes: an address that is not a web address draws no
// link, a quote in a title arrives as an entity, and an empty project
// name draws one plain heading.
func TestBoardHTMLRenderEscapes(t *testing.T) {
	html := renderUnderNode(t)["edges"].HTML

	for _, want := range []string{
		`<span class="key">FT-3</span>`,
		`say &quot;hi&quot; &lt;b&gt;`,
		`<a class="key" href="https://x.io/pull/12" rel="noopener">#12</a>`,
		`<code>/f10:review FT-1</code>`,
		"<h1>board</h1>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("%q is not in the markup:\n%s", want, html)
		}
	}

	if strings.Contains(html, "javascript:") {
		t.Error("a javascript: address reached the markup")
	}
}

// The version the page knows is the version the binary emits.
func TestBoardHTMLKnowsThisVersion(t *testing.T) {
	m := regexp.MustCompile(`var KNOWN_VERSION = (\d+);`).FindStringSubmatch(boardHTMLPage)
	if m == nil {
		t.Fatal("the page declares no KNOWN_VERSION")
	}

	if got, _ := strconv.Atoi(m[1]); got != lookaround.Version {
		t.Errorf("the page knows version %d, the board is version %d", got, lookaround.Version)
	}
}

func TestBoardFormatFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"json and html", []string{"--json", "--html"}, "exclusive"},
		{"out alone", []string{"--out", "board.html"}, "--out writes"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := newApp().Run(t.Context(), append([]string{"f10", "board"}, c.args...))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want an error naming %q", err, c.want)
			}
		})
	}
}

func TestEmitBoard(t *testing.T) {
	data := []byte("<!doctype html>\n")

	var buf bytes.Buffer
	if err := emitBoard(&buf, "", data); err != nil || !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("stdout: %v, %q", err, buf.String())
	}

	path := filepath.Join(t.TempDir(), "board.html")
	if err := emitBoard(&buf, path, data); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("file: %v, %q", err, got)
	}
}
