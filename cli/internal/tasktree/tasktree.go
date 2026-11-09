// Package tasktree draws every task tree f10 prints. Skills relay this output
// verbatim instead of drawing the glyphs themselves, so a tree reads the same
// wherever it is shown.
package tasktree

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/textx"
)

// A Cell is one column of a row: Text is measured and padded, Render prints
// in its place, so escape sequences never count as columns.
type Cell struct {
	Text   string
	Render func(text string) string
	// Fill cuts the cell to the columns Width leaves after the rest of its
	// row, and pads neither it nor the cells after it, so they follow it.
	Fill bool
}

// A Note is a labelled line under a row: an ask, a halt, a gate. Whole
// keeps it uncut, for text a reader acts on (a url, a question).
type Note struct {
	Label string
	Text  string
	Whole bool
}

// A Node is one task. Depth 0 is the top level; a node one deeper than the
// node before it hangs under that node.
type Node struct {
	Depth int
	Cells []Cell // the first is the task's id
	Title string
	Notes []Note
}

// A Tree is how the nodes print.
type Tree struct {
	// Head prints first, alone, and the top-level nodes hang under it.
	Head string
	// Indent goes before every line but Head.
	Indent string
	// Hang draws glyphs on the top-level nodes too, as if under a Head.
	Hang bool
	// Align pads each column to its widest cell; otherwise the cells of a
	// row are joined by two spaces, empty ones skipped.
	Align bool
	// Width cuts titles and notes to fit it; 0 is no limit.
	Width int
	// Label renders a note's label; nil prints it as is.
	Label func(string) string
	// Columns are minimum column widths under Align, so trees written one
	// after another line up as one (Measure).
	Columns []int
}

// Write prints the nodes, an empty list printing nothing but Head.
func (t Tree) Write(w io.Writer, nodes []Node) error {
	var b strings.Builder

	if t.Head != "" {
		b.WriteString(t.Head + "\n")
	}

	leads, conts := t.leadsOf(nodes)
	widths := t.columns(nodes, leads)

	for i, n := range nodes {
		t.row(&b, leads[i], n.Cells, widths)

		id := ""
		if len(n.Cells) > 0 {
			id = n.Cells[0].Text
		}

		// title and notes start under the column after the row's own id
		text := width(leads[i]) + width(id) + 2
		for _, note := range n.Notes {
			text = max(text, width(conts[i])+1+width(note.Label)+2)
		}

		if n.Title != "" {
			b.WriteString(t.Indent + pad(conts[i], text) + t.cut(n.Title, text) + "\n")
		}

		for _, note := range n.Notes {
			label := note.Label
			if t.Label != nil {
				label = t.Label(label)
			}

			gap := strings.Repeat(" ", text-2-width(note.Label)-width(conts[i]))
			value := note.Text
			if !note.Whole {
				value = t.cut(value, text)
			}

			b.WriteString(t.Indent + conts[i] + gap + label + "  " + value + "\n")
		}
	}

	_, err := io.WriteString(w, b.String())
	if err != nil {
		return fmt.Errorf("writing the tree: %w", err)
	}

	return nil
}

// Measure is the column widths Write pads nodes to, for trees written one
// after another that must line up (Columns).
func (t Tree) Measure(nodes []Node) []int {
	leads, _ := t.leadsOf(nodes)

	return t.measure(nodes, leads)
}

// leadsOf draws each node's glyphs: the lead before its row and the
// continuation before its title and notes.
func (t Tree) leadsOf(nodes []Node) ([]string, []string) {
	shift := 0
	if t.Head != "" || t.Hang {
		shift = 1
	}

	depth := make([]int, len(nodes))
	for i, n := range nodes {
		depth[i] = n.Depth + shift
	}

	last := make([]bool, len(nodes))
	kids := make([]bool, len(nodes))

	for i := range nodes {
		last[i] = true
		kids[i] = i+1 < len(nodes) && depth[i+1] > depth[i]

		for j := i + 1; j < len(nodes) && depth[j] >= depth[i]; j++ {
			if depth[j] == depth[i] {
				last[i] = false

				break
			}
		}
	}

	leads := make([]string, len(nodes))
	conts := make([]string, len(nodes))

	var open []bool // per depth: a sibling still follows below

	for i := range nodes {
		d := depth[i]
		open = open[:min(len(open), d)]

		var up strings.Builder

		for l := 1; l < d; l++ {
			if l < len(open) && open[l] {
				up.WriteString("│  ")
			} else {
				up.WriteString("   ")
			}
		}

		leads[i], conts[i] = up.String(), up.String()

		if d > 0 {
			if last[i] {
				leads[i] += "└─ "
				conts[i] += "   "
			} else {
				leads[i] += "├─ "
				conts[i] += "│  "
			}
		}

		if kids[i] {
			conts[i] += "│"
		}

		for len(open) <= d {
			open = append(open, false)
		}

		open[d] = !last[i]
	}

	return leads, conts
}

func (t Tree) columns(nodes []Node, leads []string) []int {
	if !t.Align {
		return nil
	}

	widths := t.measure(nodes, leads)
	for c, w := range t.Columns {
		if c == len(widths) {
			widths = append(widths, 0)
		}

		widths[c] = max(widths[c], w)
	}

	return widths
}

// measure skips a Fill cell and those after it, which are never padded.
func (t Tree) measure(nodes []Node, leads []string) []int {
	var widths []int

	for i, n := range nodes {
		for c, cell := range n.Cells {
			if cell.Fill || slices.ContainsFunc(n.Cells[:c], func(p Cell) bool { return p.Fill }) {
				break
			}

			if c == len(widths) {
				widths = append(widths, 0)
			}

			cw := width(cell.Text)
			if c == 0 {
				cw += width(leads[i])
			}

			widths[c] = max(widths[c], cw)
		}
	}

	return widths
}

func (t Tree) row(b *strings.Builder, lead string, cells []Cell, widths []int) {
	var line strings.Builder

	line.WriteString(lead)

	fill := slices.IndexFunc(cells, func(c Cell) bool { return c.Fill })

	for c, cell := range cells {
		if widths == nil && cell.Text == "" {
			continue
		}

		if c > 0 {
			line.WriteString("  ")
		}

		if c == fill {
			cell.Text = t.fit(cell.Text, lead, cells, widths, fill)
		}

		line.WriteString(cell.render())

		if widths != nil && (fill < 0 || c < fill) {
			cw := width(cell.Text)
			if c == 0 {
				cw += width(lead)
			}

			line.WriteString(strings.Repeat(" ", max(0, widths[c]-cw)))
		}
	}

	b.WriteString(t.Indent + strings.TrimRight(line.String(), " ") + "\n")
}

// fit cuts the Fill cell to what Width leaves after the rest of the row.
func (t Tree) fit(text, lead string, cells []Cell, widths []int, fill int) string {
	if t.Width <= 0 {
		return text
	}

	used := width(t.Indent) + width(lead)

	for c, cell := range cells {
		if c == fill || cell.Text == "" && (widths == nil || c > fill) {
			continue
		}

		cw := width(cell.Text)
		if widths != nil && c < fill {
			cw = widths[c]
			if c == 0 {
				cw -= width(lead)
			}
		}

		used += cw + 2
	}

	return textx.Cut(text, max(10, t.Width-used))
}

func (c Cell) render() string {
	if c.Render == nil {
		return c.Text
	}

	return c.Render(c.Text)
}

func (t Tree) cut(s string, at int) string {
	if t.Width <= 0 {
		return s
	}

	return textx.Cut(s, max(10, t.Width-width(t.Indent)-at))
}

func width(s string) int { return textx.Width(s) }

func pad(s string, n int) string {
	return s + strings.Repeat(" ", max(0, n-width(s)))
}
