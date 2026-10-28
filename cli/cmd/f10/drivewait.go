package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
)

// wakeStep is how often a waiting drive looks for a write: a step boundary,
// a prompt mark or a cancel is seen within it.
var wakeStep = time.Second

// wake waits up to d, and returns sooner once a file in the state dir or
// the drive's record is written after the wait began: a step ending, an
// agent taking a prompt, a cancel. A stat a second of a few dozen small
// files costs nothing, and watches in-place writes a directory watch misses.
func (in *driveInput) wake(ctx context.Context, d time.Duration) error {
	start := time.Now()
	deadline := start.Add(d)

	var record string
	if in.rec != nil && in.root != "" {
		record = drives.Path(in.root, in.rec.Number)
	}

	for {
		step := min(wakeStep, time.Until(deadline))
		if step <= 0 {
			return nil
		}

		if err := sleepCtx(ctx, step); err != nil {
			return err
		}

		if in.draining.Load() || written(in.stateDir, record, start) {
			return nil
		}
	}
}

// written reports a file in dir, or the file record, modified after since.
func written(dir, record string, since time.Time) bool {
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

		if fi, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && fi.ModTime().After(since) {
			return true
		}
	}

	return false
}
