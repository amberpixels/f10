package statusview

import "github.com/amberpixels/f10/cli/internal/lore"

// ReadOutcomes reads lore first and the findings and verdict files for
// whatever lore holds nothing of: runs from before the steps wrote lore.
func ReadOutcomes(store *lore.Store, storageRoot, task string) Outcomes {
	return SeriesOutcomes(store, storageRoot, task, true)
}
