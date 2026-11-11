package statusview

import (
	"slices"
	"strconv"
	"strings"
)

// LoreSeries is the changes a show of id reads: every <id>.<n> newest first,
// then id itself; a change named with its number, or a task with one PR,
// is that change alone.
func LoreSeries(changes []string, id string) []string {
	var later []int

	for _, c := range changes {
		rest, ok := strings.CutPrefix(c, id+".")
		if n, err := strconv.Atoi(rest); ok && err == nil && n > 1 {
			later = append(later, n)
		}
	}

	slices.Sort(later)
	slices.Reverse(later)

	out := make([]string, 0, len(later)+1)
	for _, n := range later {
		out = append(out, id+"."+strconv.Itoa(n))
	}

	return append(out, id)
}
