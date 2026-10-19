package observe

import (
	"path/filepath"
	"strings"
)

// inRepoStorage is the storage root's name inside a checkout. A path holding
// it names its own root, so a write is recognised even when the storage root
// was not resolved for the hook's cwd.
const inRepoStorage = "/.f10/"

// FromWrite reads one file written with Write or Edit. storageRoot is the
// checkout's resolved storage root, in-repo or out-of-tree; empty falls back
// to the `.f10/` the path itself holds. Plan archives are moves, not new
// plans, and are not matched. Meta is left for the caller to Stamp.
func FromWrite(path, storageRoot string) Observation {
	roots := []string{}
	if storageRoot != "" {
		roots = append(roots, filepath.Clean(storageRoot))
	}

	if i := strings.LastIndex(path, inRepoStorage); i >= 0 {
		roots = append(roots, path[:i+len(inRepoStorage)-1])
	}

	for _, root := range roots {
		rel, ok := strings.CutPrefix(filepath.Clean(path), root+string(filepath.Separator))
		if !ok || !strings.HasSuffix(rel, ".md") {
			continue
		}

		parts := strings.Split(filepath.ToSlash(rel), "/")

		switch {
		case len(parts) == 2 && parts[0] == "plans":
			return &PlanWritten{Path: path, Task: strings.TrimSuffix(parts[1], ".md")}
		case len(parts) == 3 && parts[0] == "reviews":
			return &FindingsWritten{Path: path, Task: parts[1], Round: strings.TrimSuffix(parts[2], ".md")}
		}
	}

	return nil
}
