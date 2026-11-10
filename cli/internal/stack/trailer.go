package stack

import "github.com/amberpixels/f10/cli/internal/lore"

// ChangeTrailer names a later series' lore change, since a squash drops the
// branch's own trailers and Task: alone leads lore why to the first PR.
func ChangeTrailer(task, change string) string {
	if change == "" || change == task {
		return ""
	}

	return lore.TrailerChange + ": " + change + "\n"
}
