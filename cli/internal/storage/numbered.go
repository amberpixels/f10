package storage

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Numbered is dir's `<n>.md` files by n, so 10.md sorts after 9.md: the
// shape of a findings series and of a verdict series. A missing dir has none.
func Numbered(dir string) []string {
	type file struct {
		n    int
		path string
	}

	entries, _ := os.ReadDir(dir)

	var files []file

	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || e.IsDir() {
			continue
		}

		if n, err := strconv.Atoi(name); err == nil {
			files = append(files, file{n, filepath.Join(dir, e.Name())})
		}
	}

	slices.SortFunc(files, func(a, b file) int { return cmp.Compare(a.n, b.n) })

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}

	return paths
}

// NumberOf is the n of a `<n>.md` path, -1 for any other name.
func NumberOf(path string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(path), ".md"))
	if err != nil || !strings.HasSuffix(path, ".md") {
		return -1
	}

	return n
}
