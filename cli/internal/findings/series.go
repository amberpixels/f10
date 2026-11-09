package findings

import "github.com/amberpixels/f10/cli/internal/storage"

// Series is the round files in dir, round one first. Parsing is left to the
// caller, because each treats a file that does not parse its own way.
func Series(dir string) []string { return storage.Numbered(dir) }

// Latest is the highest round's file in dir, or "".
func Latest(dir string) string {
	if files := Series(dir); len(files) > 0 {
		return files[len(files)-1]
	}

	return ""
}
