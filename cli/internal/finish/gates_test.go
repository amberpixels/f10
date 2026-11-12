package finish

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A follow-up PR shows only its own series: the first PR's lore verdict and
// its verdict file stay out of its gates, while a finished task with no
// branch still shows the series it ended with.
func TestStatusReadsTheCurrentSeries(t *testing.T) {
	storage := t.TempDir()
	testkit.WriteFile(t, filepath.Join(storage, "verdicts", "FT-1", "1.md"),
		"task: FT-1\nsubject: plan FT-1\nverdict: proceed\nreason: first PR\n")

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(storage, "verdicts", "FT-1", "1.md"), old, old); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	store := &lore.Store{Root: root}
	testkit.WriteLore(t, root, lore.Entry{
		Kind: lore.KindVerdict, Change: "FT-1", Human: "h@x", At: old,
		Outcome: "proceed", Gate: "judge", Commit: "abcdef1", Judged: "change", Body: "first PR",
	})

	if _, err := CloseSeries(
		filepath.Join(storage, "reviews", "FT-1"),
		archive.SeriesEnd{PR: 7},
	); err != nil {
		t.Fatal(err)
	}

	if o := statusview.SeriesOutcomes(store, storage, "FT-1", true); o.ChangeJudge != nil || o.PlanJudge != nil {
		t.Errorf("the follow-up shows the first PR's verdicts: %+v", o)
	}

	if o := statusview.SeriesOutcomes(
		store,
		storage,
		"FT-1",
		false,
	); o.ChangeJudge == nil ||
		o.ChangeJudge.Reason != "first PR" {
		t.Errorf("the finished task lost its last series: %+v", o)
	}
}
