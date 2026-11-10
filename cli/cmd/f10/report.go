package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
