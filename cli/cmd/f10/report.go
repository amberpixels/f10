package main

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

type fact struct {
	label string
	value string
	url   bool
	path  bool // a markdown file, linked on a terminal; the marker stays blank

	// run marks a run row: its value takes style and wraps at the page width.
	// A facts value does neither, since its link escapes would be cut apart.
	run   bool
	style lipgloss.Style
}

// writeReport prints the facts block as conventions/report.md fixes it; notes
// follow as prose beneath.
func writeReport(w io.Writer, rows []fact, notes []string) error {
	if err := writeFacts(w, rows, labelWidth(rows), 0); err != nil {
		return err
	}

	for _, n := range notes {
		if _, err := fmt.Fprintln(w, n); err != nil {
			return err
		}
	}

	return nil
}

// labelWidth is the label column rows need, the colon included.
func labelWidth(rows []fact) int {
	width := 0
	for _, r := range rows {
		width = max(width, visibleWidth(r.label)+1)
	}

	return width
}

// writeFacts prints rows as `label:  <marker> value`, labels padded to width,
// so rows printed apart with one width still share a value column.
func writeFacts(w io.Writer, rows []fact, width, limit int) error {
	links := linkTerminal(w)
	indent := strings.Repeat(" ", width+4)

	for _, r := range rows {
		marker := " "
		if r.url {
			marker = "↗"
		}

		value := r.value
		if r.path && links {
			value = fileLink(value)
		}

		segments := []string{value}
		if r.run {
			segments = wrap(value, limit-len(indent))
			for i, s := range segments {
				segments[i] = r.style.Render(s)
			}
		}

		if _, err := fmt.Fprintf(w, "%s  %s %s\n", padRight(r.label+":", width), marker, segments[0]); err != nil {
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

// follow acts on a url a driver returned; drivers only return addresses.
func follow(ctx context.Context, w io.Writer, dir, url string, linkOnly bool) error {
	if linkOnly {
		_, err := fmt.Fprintln(w, url)

		return err
	}

	return ui.Open(ctx, dir, url)
}

// writeDoc pages markdown on a terminal and writes it raw to a pipe: no
// renderer is bundled, since glow, bat or an editor compose for free.
func writeDoc(ctx context.Context, w io.Writer, dir, doc string) error {
	doc = strings.TrimRight(doc, "\n") + "\n"

	pager := strings.Fields(os.Getenv("PAGER"))
	if !ui.IsTerminal(w) || len(pager) == 0 {
		_, err := io.WriteString(w, doc)

		return err
	}

	return shell.Pipe(ctx, dir, doc, pager[0], pager[1:]...)
}

// linkFlag is the same flag on every verb that follows an address, so `open`
// keeps one meaning across nouns whose addresses are paths and urls alike.
func linkFlag() cli.Flag {
	return &cli.BoolFlag{Name: "link", Usage: "print the address instead of opening it"}
}

func printFlag() cli.Flag {
	return &cli.BoolFlag{Name: "print", Usage: "write the item, or with none the list's first page, to stdout"}
}
