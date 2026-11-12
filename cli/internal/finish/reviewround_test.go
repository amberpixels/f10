package finish

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/archive"
)

// A merge closes a series with no rounds too, since the archive counts the
// task's PRs; finishing the same PR again closes nothing more.
func TestCloseSeriesCountsEveryPR(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reviews", "DM-5")
	first := archive.SeriesEnd{PR: 7, Head: "abc1234"}

	if got := archive.ChangeOf(dir); got != "DM-5" {
		t.Fatalf("no series closed: %q", got)
	}

	for range 2 {
		if _, err := CloseSeries(dir, first); err != nil {
			t.Fatal(err)
		}
	}

	if got := archive.ChangeOf(dir); got != "DM-5.2" {
		t.Errorf("after one PR, twice finished: %q, want DM-5.2", got)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	got, err := CloseSeries(dir, archive.SeriesEnd{PR: 8, Head: "def5678"})
	if err != nil || got != filepath.Join(root, "reviews", "archive", "DM-5.2") {
		t.Fatalf("a second PR's rounds: %q, %v", got, err)
	}

	if e := archive.ReadSeriesEnd(got); e.PR != 8 || e.Head != "def5678" || e.At.IsZero() {
		t.Errorf("closed file reads %+v", e)
	}

	if _, err := CloseSeries(dir, archive.SeriesEnd{PR: 8}); err != nil || archive.ChangeOf(dir) != "DM-5.3" {
		t.Errorf("glab's !8 and gh's #8 are one PR: %q, %v", archive.ChangeOf(dir), err)
	}
}
