// Package textx holds the small text helpers every command shares, so a
// column is cut, an empty value is marked and an age is spelled one way.
package textx

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// Cut shortens s to n terminal columns, an ellipsis marking the cut.
// Columns, not bytes or runes, because a wide character takes two. An n
// below 1 leaves s whole, since a caller with no width has no limit.
func Cut(s string, n int) string {
	if n < 1 || Width(s) <= n {
		return s
	}

	return runewidth.Truncate(s, n, "…")
}

// Width is how many terminal columns s takes.
func Width(s string) int { return runewidth.StringWidth(s) }

// OrDash marks an empty value, so a column never collapses.
func OrDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// Age is a duration read at a glance: seconds under a minute, then the
// largest whole unit that fits (12m, 5h, 3d). A future time is 0s.
func Age(now, then time.Time) string {
	d := max(now.Sub(then).Round(time.Second), 0)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// Plural is one for a count of 1, else many.
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}

// AppendNote appends note unless it is empty.
func AppendNote(notes []string, note string) []string {
	if note == "" {
		return notes
	}

	return append(notes, note)
}

// FirstLine is s up to its first newline.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}
