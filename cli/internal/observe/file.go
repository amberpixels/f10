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
// resolved storage root; empty falls back to the `.f10/` the path holds.
// Plan archives are moves, not new plans, and are not matched.
func FromWrite(path, storageRoot string) Observation {
	for _, root := range storageRoots(path, storageRoot) {
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
		case len(parts) == 3 && parts[0] == "verdicts":
			return &VerdictWritten{Path: path, Key: parts[1], N: strings.TrimSuffix(parts[2], ".md")}
		}
	}

	return nil
}
