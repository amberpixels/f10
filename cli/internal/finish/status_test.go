package finish

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Every verdict of the open series and every findings file gets a row; an
// earlier PR's verdict stays out, and a subject judged twice takes numbers.
func TestShipFiles(t *testing.T) {
	storage := t.TempDir()
	verdicts := filepath.Join(storage, "verdicts", "FT-1")

	testkit.WriteFile(
		t,
		filepath.Join(verdicts, "1.md"),
		"task: FT-1\nsubject: task FT-1\nverdict: proceed\nreason: old\n",
	)

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(verdicts, "1.md"), old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := CloseSeries(
		filepath.Join(storage, "reviews", "FT-1"),
		archive.SeriesEnd{PR: 7},
	); err != nil {
		t.Fatal(err)
	}

	testkit.WriteFile(
		t,
		filepath.Join(verdicts, "2.md"),
		"task: FT-1\nsubject: plan FT-1\nverdict: proceed\nreason: a\n",
	)
	testkit.WriteFile(
		t,
		filepath.Join(verdicts, "3.md"),
		"task: FT-1\nsubject: branch x\nverdict: proceed\nreason: b\n",
	)
	testkit.WriteFile(t, filepath.Join(verdicts, "4.md"), "task: FT-1\nsubject: pr 9\nverdict: proceed\nreason: c\n")
	testkit.WriteFile(t, filepath.Join(storage, "reviews", "FT-1", "1.md"), "# Review\n")
	testkit.WriteFile(t, filepath.Join(storage, "reviews", "FT-1", "2.md"), "# Review\n")

	s := statusview.Shipment{Task: "FT-1"}
	s.Verdicts, s.Reviews = statusview.ShipFiles(storage, "FT-1")

	var out bytes.Buffer
	if err := render.WriteReport(&out, s.Facts(false), nil); err != nil {
		t.Fatal(err)
	}

	want := "task:              FT-1\n" +
		"judge plan:        " + filepath.Join(verdicts, "2.md") + "\n" +
		"judge change 3:    " + filepath.Join(verdicts, "3.md") + "\n" +
		"judge change 4:    " + filepath.Join(verdicts, "4.md") + "\n" +
		"review 1:          " + filepath.Join(storage, "reviews", "FT-1", "1.md") + "\n" +
		"review 2:          " + filepath.Join(storage, "reviews", "FT-1", "2.md") + "\n"
	if out.String() != want {
		t.Errorf("block =\n%s\nwant\n%s", out.String(), want)
	}
}
