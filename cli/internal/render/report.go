// Package render is the output layer every command shares: the facts block,
// task links and hyperlinks, and the pager.
package render

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/ui"
)

type Fact struct {
	Label string
	Value string
	URL   bool
	Path  bool // a markdown file, linked on a terminal; the marker stays blank

	// Run marks a run row: its value takes style and wraps at the page width.
	// A facts value does neither, since its link escapes would be cut apart.
	Run   bool
	Style lipgloss.Style
}

// WriteReport prints the facts block as conventions/report.md fixes it; notes
// follow as prose beneath.
func WriteReport(w io.Writer, rows []Fact, notes []string) error {
	if err := WriteFacts(w, rows, LabelWidth(rows), 0); err != nil {
		return err
	}

	for _, n := range notes {
		if _, err := fmt.Fprintln(w, n); err != nil {
			return err
		}
	}

	return nil
}

// LabelWidth is the label column rows need, the colon included.
func LabelWidth(rows []Fact) int {
	width := 0
	for _, r := range rows {
		width = max(width, VisibleWidth(r.Label)+1)
	}

	return width
}

// WriteFacts prints rows as `label:  <marker> value`, labels padded to width,
// so rows printed apart with one width still share a value column.
func WriteFacts(w io.Writer, rows []Fact, width, limit int) error {
	links := LinkTerminal(w)
	indent := strings.Repeat(" ", width+4)

	for _, r := range rows {
		marker := " "
		if r.URL {
			marker = "↗"
		}

		value := r.Value
		if r.Path && links {
			value = FileLink(value)
		}

		segments := []string{value}
		if r.Run {
			segments = Wrap(value, limit-len(indent))
			for i, s := range segments {
				segments[i] = r.Style.Render(s)
			}
		}

		if _, err := fmt.Fprintf(w, "%s  %s %s\n", PadRight(r.Label+":", width), marker, segments[0]); err != nil {
			return err
		}

		for _, s := range segments[1:] {
			if _, err := fmt.Fprintf(w, "%s%s\n", indent, s); err != nil {
				return err
			}
		}
	}

	return nil
}

// Follow acts on a url a driver returned; drivers only return addresses.
func Follow(ctx context.Context, w io.Writer, dir, url string, linkOnly bool) error {
	if linkOnly {
		_, err := fmt.Fprintln(w, url)

		return err
	}

	return ui.Open(ctx, dir, url)
}

// WriteDoc pages markdown on a terminal and writes it raw to a pipe: no
// renderer is bundled, since glow, bat or an editor compose for free.
func WriteDoc(ctx context.Context, w io.Writer, dir, doc string) error {
	doc = strings.TrimRight(doc, "\n") + "\n"

	pager := strings.Fields(os.Getenv("PAGER"))
	if !ui.IsTerminal(w) || len(pager) == 0 {
		_, err := io.WriteString(w, doc)

		return err
	}

	return shell.Pipe(ctx, dir, doc, pager[0], pager[1:]...)
}

// LinkFlag is the same flag on every verb that follows an address, so `open`
// keeps one meaning across nouns whose addresses are paths and urls alike.
func LinkFlag() cli.Flag {
	return &cli.BoolFlag{Name: "link", Usage: "print the address instead of opening it"}
}

func PrintFlag() cli.Flag {
	return &cli.BoolFlag{Name: "print", Usage: "write the item, or with none the list's first page, to stdout"}
}
