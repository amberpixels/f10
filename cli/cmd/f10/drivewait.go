package main

import (
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
var wakeStep = time.Second

// wake waits up to d, and returns sooner once the drive's record, or a
// per-session file about one of its worktrees (a run file, a prompt mark,
// an open wait), is written after the wait began. Other sessions' writes
// to the shared state dir do not wake it. A stat a second of a few dozen
// small files costs nothing, and sees in-place writes a directory watch
// misses.
func (in *driveInput) wake(ctx context.Context, d time.Duration) error {
	start := time.Now()
	deadline := start.Add(d)

	var record string
	if in.rec != nil && in.root != "" {
		record = drives.Path(in.root, in.rec.Number)
	}

	var roots []string

	for _, r := range in.runs {
		if r.active() && r.o.path != "" {
			roots = append(roots, r.o.path)
		}
	}

	for {
		step := min(wakeStep, time.Until(deadline))
		if step <= 0 {
			return nil
		}

		if err := sleepCtx(ctx, step); err != nil {
			return err
		}

		if in.draining.Load() || written(in.stateDir, record, roots, start) {
			return nil
		}
	}
}

// written reports the file record, or a file in dir about one of roots,
// modified after since.
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
