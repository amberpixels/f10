package drive

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/state"
)

// wakeStep is how often a waiting drive looks for a write: a step boundary,
// a prompt mark or a cancel is seen within it.
const wakeStep = time.Second

// Wake returns before d once the drive's record or a per-session file about
// one of its worktrees is written. A stat a second of a few dozen small
// files costs nothing, and sees in-place writes a directory watch misses.
func (in *DriveInput) Wake(ctx context.Context, d time.Duration) error {
	started := time.Now()
	deadline := started.Add(d)

	var record string
	if in.Rec != nil && in.Root != "" {
		record = drives.Path(in.Root, in.Rec.Number)
	}

	var roots []string

	for _, r := range in.Runs {
		if r.active() && r.O.Path != "" {
			roots = append(roots, r.O.Path)
		}
	}

	for {
		step := min(cmp.Or(in.WakeStep, wakeStep), time.Until(deadline))
		if step <= 0 {
			return nil
		}

		if err := sleepCtx(ctx, step); err != nil {
			return err
		}

		if in.Draining.Load() || written(in.StateDir, record, roots, started) {
			return nil
		}
	}
}

func written(dir, record string, roots []string, since time.Time) bool {
	if record != "" {
		if fi, err := os.Stat(record); err == nil && fi.ModTime().After(since) {
			return true
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		fi, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil || !fi.ModTime().After(since) {
			continue
		}

		if root := state.FileRoot(dir, e.Name()); root != "" && slices.Contains(roots, root) {
			return true
		}
	}

	return false
}
