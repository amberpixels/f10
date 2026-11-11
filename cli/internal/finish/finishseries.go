package finish

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// seriesLore sets in.change to the lore change the closed series names, then
// returns the report's lore line ("" with no store).
func seriesLore(in *FinishInput, end *archive.SeriesEnd) string {
	if end != nil {
		if change, ok := ClosedChange(in.Storage.Reviews(in.Task.ID), *end); ok {
			in.Change = change
		}
	}

	if in.LoreStore == nil {
		return ""
	}

	return lore.Note(in.LoreStore, in.loreChange())
}

// CloseSeries ends the task's series even when it had no review rounds,
// since the archive is what counts a task's PRs for its lore change; a series
// already closed with the same PR or head (finish run again) adds none.
func CloseSeries(dir string, end archive.SeriesEnd) (string, error) {
	if fsx.Exists(dir) {
		return archive.ArchiveReviews(dir, end)
	}

	archived := storage.ArchiveOf(filepath.Dir(dir))
	for _, n := range archive.SeriesNumbers(archived, filepath.Base(dir)) {
		if end.Same(archive.ReadSeriesEnd(filepath.Join(archived, fmt.Sprintf("%s.%d", filepath.Base(dir), n)))) {
			return "", nil
		}
	}

	target := archive.NextSeries(archived, filepath.Base(dir))
	if err := os.MkdirAll(target, 0o750); err != nil {
		return "", fmt.Errorf("closing the review series at %s: %w", dir, err)
	}

	return "", end.Write(target)
}

// ClosedChange is the change of the PR end closed, when the newest archived
// PR is that one: a finish run again after its merge reports on it, not on
// the next series.
func ClosedChange(dir string, end archive.SeriesEnd) (string, bool) {
	closed := archive.ClosedPRs(dir)
	if len(closed) == 0 || !end.Same(archive.ReadSeriesEnd(closed[len(closed)-1])) {
		return "", false
	}

	return archive.SeriesChange(filepath.Base(dir), len(closed)), true
}
