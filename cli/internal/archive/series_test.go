package archive

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A closed file an older binary wrote names its PR as #7 or !7, and still
// reads as the same series as one naming it bare.
func TestReadSeriesEndReadsAnOlderFile(t *testing.T) {
	t.Parallel()

	for _, pr := range []string{"#7", "!7", "7"} {
		dir := t.TempDir()

		body := "pr: " + pr + "\nhead: abc\nclosed: 2026-11-01T10:00:00Z\n"
		if err := os.WriteFile(filepath.Join(dir, seriesClosedFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}

		e := ReadSeriesEnd(dir)
		if e.PR != 7 || e.Head != "abc" || !e.Same(SeriesEnd{PR: 7}) {
			t.Errorf("pr: %s reads %+v", pr, e)
		}
	}
}

// A series with no PR writes none and reads back as 0, so it never matches
// another series by number.
func TestSeriesEndWithNoPR(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := (SeriesEnd{Head: "abc"}).Write(dir); err != nil {
		t.Fatal(err)
	}

	if e := ReadSeriesEnd(dir); e.PR != 0 || e.Same(SeriesEnd{Head: "def"}) {
		t.Errorf("a series with no PR reads %+v", e)
	}
}

// A show of a task reads its later PRs' changes newest first, then the
// task's own; a longer id sharing the prefix is another task.
func TestLoreSeries(t *testing.T) {
	t.Parallel()

	got := LoreSeries([]string{"FT-1", "FT-1.2", "FT-1.3", "FT-12", "FT-1.x", "FT-1.1"}, "FT-1")
	if want := []string{"FT-1.3", "FT-1.2", "FT-1"}; !slices.Equal(got, want) {
		t.Errorf("LoreSeries = %v, want %v", got, want)
	}
}
