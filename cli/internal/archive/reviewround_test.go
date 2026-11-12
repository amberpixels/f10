package archive_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/archive"
)

// Each finished series takes the next number; a task with no findings
// archives nothing.
func TestArchiveReviewsNumbersEachSeries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reviews", "DM-5")

	if got, err := archive.ArchiveReviews(dir, archive.SeriesEnd{}); got != "" || err != nil {
		t.Fatalf("no directory: got %q, %v", got, err)
	}

	for _, want := range []string{"DM-5.1", "DM-5.2"} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}

		got, err := archive.ArchiveReviews(dir, archive.SeriesEnd{})
		if err != nil || got != filepath.Join(root, "reviews", "archive", want) {
			t.Errorf("got %q, %v; want archive/%s", got, err, want)
		}
	}
}
