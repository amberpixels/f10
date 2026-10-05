package main

import (
	"fmt"
	"io"
)

// A fact is one row of the facts block: a label, a value, and whether the
// value is a url, which earns the marker cell.
type fact struct {
	label string
	value string
	url   bool
}

// writeReport prints the facts block conventions/report.md fixes: labels
// padded to the widest including its colon, two spaces, the marker cell
// (the arrow for a url, blank otherwise), one space, the value. Notes
// follow as prose beneath the block.
func writeReport(w io.Writer, rows []fact, notes []string) error {
	width := 0
	for _, r := range rows {
		width = max(width, len(r.label)+1)
	}

	for _, r := range rows {
		marker := " "
		if r.url {
			marker = "↗"
		}

		if _, err := fmt.Fprintf(w, "%-*s  %s %s\n", width, r.label+":", marker, r.value); err != nil {
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
