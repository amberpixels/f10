package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

// htmlBoards is every recorded board, plus one with nothing to show.
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

	boards["empty"] = lookaround.Board{
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

// The page's model() and the terminal's boardLines agree on every board:
// same notes, same empty sentence, same lines in the same order.
func TestBoardHTMLModelMatchesTerminal(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH: the page's model is checked only where it is")
	}

	dir := t.TempDir()
	harness := filepath.Join(dir, "model.js")
	script := "const fs = require('fs');\n" +
		"const __board = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));\n" +
		scriptBody(t) + "\n" +
		"process.stdout.write(JSON.stringify(model(__board)));\n"

	if err := os.WriteFile(harness, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, b := range htmlBoards(t) {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}

			input := filepath.Join(dir, name+".json")
			if err := os.WriteFile(input, raw, 0o600); err != nil {
				t.Fatal(err)
			}

			out, err := exec.CommandContext(t.Context(), node, harness, input).CombinedOutput()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, out)
			}

			var got boardModel
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("the page's model does not parse: %v\n%s", err, out)
			}

			want := boardLines(b)
			if !reflect.DeepEqual(want, got) {
				wantJSON, _ := json.MarshalIndent(want, "", "  ")
				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				t.Errorf("the page and the terminal disagree\nterminal:\n%s\npage:\n%s", wantJSON, gotJSON)
			}
		})
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
