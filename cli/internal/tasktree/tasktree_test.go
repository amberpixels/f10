package tasktree

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func cells(texts ...string) []Cell {
	out := make([]Cell, len(texts))
	for i, s := range texts {
		out[i] = Cell{Text: s}
	}

	return out
}

func golden(t *testing.T, name string, tree Tree, nodes []Node) {
	t.Helper()

	var out bytes.Buffer
	if err := tree.Write(&out, nodes); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if out.String() != string(want) {
		t.Errorf("%s:\n%s\nwant:\n%s", name, out.String(), want)
	}
}

// A parent with nested After: chains three deep, a halted child with its
// halt and next, an asking child, an unrelated task last, and a title cut
// at the width.
func TestParent(t *testing.T) {
	golden(t, "parent", Tree{Head: "FT-209  4 done · 2 halted · 1 asking", Align: true, Width: 80}, []Node{
		{Cells: cells("✓ FT-206", "done", "finish", "#208"), Title: "Show who runs a driven finish"},
		{Cells: cells("✓ FT-210", "done", "finish", "#216"), Title: "Keep a drive as a record with a lease per task"},
		{Depth: 1, Cells: cells("✓ FT-212", "done", "finish", "#219"), Title: "Run drives in a per-user daemon"},
		{
			Depth: 2, Cells: cells("✗ FT-213", "halted", "implement"), Title: "Keep asks in an inbox",
			Notes: []Note{
				{"halt", "catchup guard: merging FT-212 conflicts in drives/record.go, docs/cli.md"},
				{"next", "/f10:catchup in this worktree, then /f10:ship FT-213"},
			},
		},
		{
			Depth: 2, Cells: cells("? FT-214", "asking", "judge"),
			Title: "Let the daemon watch tasks no drive owns, a title long enough to be cut at eighty",
			Notes: []Note{{"ask", "1. Proceed? [yes | no]"}},
		},
		{Cells: cells("✓ FT-211", "done", "finish", "#215"), Title: "Put agent control behind an interface"},
		{Cells: cells("· FT-230", "not started", ""), Title: "An unrelated task"},
	})
}

// A task alone: no glyphs, its notes' labels wider than its id column.
func TestSingle(t *testing.T) {
	golden(t, "single", Tree{Align: true}, []Node{{
		Cells: cells("✓ FT-1", "done", "finish", "#2"), Title: "One task",
		Notes: []Note{{"judge", "proceed"}, {"implement", "done"}},
	}})
}

// Unaligned cells join by two spaces, empty ones skipped; Hang and Indent
// put glyphs on the top level under a heading the caller prints.
func TestPackedAndHung(t *testing.T) {
	golden(t, "packed", Tree{}, []Node{
		{Cells: cells("FT-72", "t72")},
		{Depth: 1, Cells: cells("FT-73", "t73", "", "after FT-72")},
		{Depth: 1, Cells: cells("FT-74", "t74", "after FT-72")},
		{Depth: 2, Cells: cells("FT-75", "t75", "after FT-74")},
	})
	golden(t, "hung", Tree{Indent: "  ", Hang: true, Align: true}, []Node{
		{Cells: cells("GH-1:", "done")},
		{Cells: cells("GH-12:", "running ship")},
	})
}
