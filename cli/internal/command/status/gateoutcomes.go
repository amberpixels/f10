package status

import (
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/statusview"
)

// ReadOutcomes reads lore first and the findings and verdict files for
// whatever lore holds nothing of: runs from before the steps wrote lore.
func ReadOutcomes(store *lore.Store, storageRoot, task string) statusview.Outcomes {
	return statusview.SeriesOutcomes(store, storageRoot, task, true)
}
