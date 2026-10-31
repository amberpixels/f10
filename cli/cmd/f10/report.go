package main

import (
	"fmt"
	"io"
)

type fact struct {
	label string
	value string
	url   bool
}

// writeReport prints the facts block as conventions/report.md fixes it; notes
// follow as prose beneath.
func writeReport(w io.Writer, rows []fact, notes []string) error {
	width := 0
	for _, r := range rows {
		width = max(width, visibleWidth(r.label)+1)
	}

	for _, r := range rows {
		marker := " "
		if r.url {
			marker = "↗"
		}

		if _, err := fmt.Fprintf(w, "%s  %s %s\n", padRight(r.label+":", width), marker, r.value); err != nil {
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
