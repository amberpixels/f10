package archive

import (
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/storage"
)

// TaskHandoff is the task's handoff file.
func TaskHandoff(root storage.Root, id string) string {
	return filepath.Join(root.Handoffs(), id+".md")
}
