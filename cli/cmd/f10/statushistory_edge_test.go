package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/archive"
)

// The edge fixture's off-vocabulary verdicts (`asked`, `ask+reply`) count as
// found and as nothing else.
func TestReviewRoundsOnTheEdgeFixture(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{"1.md", "2.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "findings", "testdata", "edge", name))
		if err != nil {
			t.Fatal(err)
		}

		writeFile(t, filepath.Join(root, "reviews", "FT-1", name), string(raw))
	}

	o := fileOutcomes(root, "FT-1", time.Time{})

	want := []reviewRound{
		{Round: "1", Found: 6, Fixed: 1, Skipped: 1, Ask: 3},
		{Round: "2", Found: 4, Fixed: 1, Skipped: 1, Ask: 1},
	}
	if !slices.Equal(o.local, want) || o.localRaised != 7 {
		t.Errorf("local = %+v raised %d, want %+v raised 7", o.local, o.localRaised, want)
	}
}

// Verdict files sort by number, and an idea or a file that does not parse
// is not the task's.
func TestJudgeFilesOrderAndSkips(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "verdicts", "FT-1")

	writeFile(t, filepath.Join(dir, "9.md"), "task: FT-1\nsubject: plan FT-1\nverdict: rethink\n")
	writeFile(t, filepath.Join(dir, "10.md"), "task: FT-1\nsubject: pr #4\nverdict: proceed\n")
	writeFile(t, filepath.Join(dir, "2.md"), "subject: idea other\nverdict: stop\n")
	writeFile(t, filepath.Join(dir, "3.md"), "task: FT-1\nverdict: maybe\n")
	writeFile(t, filepath.Join(dir, "notes.md"), "task: FT-1\nverdict: stop\n")

	var got []string
	for _, v := range archive.JudgeFiles(root, "FT-1") {
		got = append(got, filepath.Base(v.Path)+" "+v.Judged)
	}

	if want := []string{"9.md plan", "10.md change"}; !slices.Equal(got, want) {
		t.Errorf("judgeFiles = %v, want %v", got, want)
	}
}
