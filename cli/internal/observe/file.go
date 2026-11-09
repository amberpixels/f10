package observe

import (
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/storage"
)

// inRepoStorage is the storage root's name inside a checkout. A path holding
// it names its own root, so a write is recognised even when the storage root
// was not resolved for the hook's cwd.
const inRepoStorage = "/" + storage.DirName + "/"

// FromWrite reads one file written with Write or Edit. storageRoot is the
// resolved storage root; empty falls back to the `.f10/` the path holds.
// Plan archives are moves, not new plans, and are not matched.
func FromWrite(path, storageRoot string) Observation {
	clean := filepath.Clean(path)
	if !strings.HasSuffix(clean, ".md") {
		return nil
	}

	dir, name := filepath.Dir(clean), strings.TrimSuffix(filepath.Base(clean), ".md")
	key, parent := filepath.Base(dir), filepath.Dir(dir)

	for _, root := range storageRoots(path, storageRoot) {
		r := storage.Root(root)

		switch {
		case dir == r.Plans():
			return &PlanWritten{Path: path, Task: name}
		case parent == r.Reviews(""):
			return &FindingsWritten{Path: path, Task: key, Round: name}
		case parent == r.Verdicts(""):
			return &VerdictWritten{Path: path, Key: key, N: name}
		}
	}

	return nil
}
