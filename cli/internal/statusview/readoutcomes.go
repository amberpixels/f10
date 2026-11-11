package statusview

import "github.com/amberpixels/f10/cli/internal/lore"

// ReadOutcomes reads lore first when store is set, and the findings and
// verdict files for whatever lore holds nothing of: runs from before the
// steps wrote lore. A nil store reads the files alone.
func ReadOutcomes(store *lore.Store, storageRoot, task string) Outcomes {
	return SeriesOutcomes(store, storageRoot, task, true)
}
