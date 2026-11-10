package statusview

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/target"
)

func LoreRootFor(t *target.Target) string {
	if t.Effective.Lore == "committed" {
		return filepath.Join(t.Layout.CheckoutRoot, ".lore")
	}

	return localLoreRoot(t.Layout)
}

// localLoreRoot is the lore dir every checkout shares.
func localLoreRoot(lay *layout.Layout) string {
	return lay.StorageRoot.Lore()
}

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
