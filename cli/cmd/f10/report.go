package main

import (
	"fmt"
	"io"
)

type fact struct {
	label string
	value string
	url   bool
	path  bool // a markdown file, linked on a terminal; the marker stays blank
}

// writeReport prints the facts block as conventions/report.md fixes it; notes
// follow as prose beneath.
func writeReport(w io.Writer, rows []fact, notes []string) error {
	links := linkTerminal(w)

	width := 0
	for _, r := range rows {
		width = max(width, visibleWidth(r.label)+1)
	}

	for _, r := range rows {
		marker := " "
		if r.url {
			marker = "↗"
		}

		value := r.value
		if r.path && links {
			value = fileLink(value)
		}

		if _, err := fmt.Fprintf(w, "%s  %s %s\n", padRight(r.label+":", width), marker, value); err != nil {
			return err
		}
	}

	for _, n := range notes {
		if _, err := fmt.Fprintln(w, n); err != nil {
			return err
		}
	}

	return nil
}
