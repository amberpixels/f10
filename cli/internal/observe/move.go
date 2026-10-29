package observe

import (
	"os"
	"path/filepath"
	"strings"
)

// FromMoves reads the `mv` commands in a Bash call for plans moved into
// plans/archive/, counted only when the archived file exists afterwards.
// Relative paths resolve against cwd; storageRoot is matched as in FromWrite.
func FromMoves(command, cwd, storageRoot string) []Observation {
	var obs []Observation

	for _, words := range Segments(command) {
		words = stripPrefix(words)
		if len(words) < 3 || filepath.Base(words[0]) != "mv" {
			continue
		}

		args := positional(words[1:])
		if len(args) < 2 {
			continue
		}

		dest := abs(cwd, args[len(args)-1])

		for _, src := range args[:len(args)-1] {
			src = abs(cwd, src)

			to := dest
			if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
				to = filepath.Join(dest, filepath.Base(src))
			}

			if o := planArchive(src, to, storageRoot); o != nil {
				obs = append(obs, o)
			}
		}
	}

	return obs
}

func planArchive(src, to, storageRoot string) Observation {
	if _, err := os.Stat(to); err != nil {
		return nil
	}

	for _, root := range storageRoots(src, storageRoot) {
		plans := filepath.Join(root, "plans")
		if filepath.Dir(src) != plans || filepath.Dir(to) != filepath.Join(plans, "archive") ||
			!strings.HasSuffix(src, ".md") {
			continue
		}

		return &PlanArchived{From: src, To: to, Task: strings.TrimSuffix(filepath.Base(src), ".md")}
	}

	return nil
}

// storageRoots is the resolved root, then the `.f10/` the path itself holds.
func storageRoots(path, storageRoot string) []string {
	roots := []string{}
	if storageRoot != "" {
		roots = append(roots, filepath.Clean(storageRoot))
	}

	if i := strings.LastIndex(path, inRepoStorage); i >= 0 {
		roots = append(roots, path[:i+len(inRepoStorage)-1])
	}

	return roots
}

func abs(cwd, path string) string {
	if filepath.IsAbs(path) || cwd == "" {
		return filepath.Clean(path)
	}

	return filepath.Join(cwd, path)
}
