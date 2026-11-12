package archive_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Verdict files sort by number, and an idea or a file that does not parse
// is not the task's.
func TestJudgeFilesOrderAndSkips(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "verdicts", "FT-1")

	testkit.WriteFile(t, filepath.Join(dir, "9.md"), "task: FT-1\nsubject: plan FT-1\nverdict: rethink\n")
	testkit.WriteFile(t, filepath.Join(dir, "10.md"), "task: FT-1\nsubject: pr #4\nverdict: proceed\n")
	testkit.WriteFile(t, filepath.Join(dir, "2.md"), "subject: idea other\nverdict: stop\n")
	testkit.WriteFile(t, filepath.Join(dir, "3.md"), "task: FT-1\nverdict: maybe\n")
	testkit.WriteFile(t, filepath.Join(dir, "notes.md"), "task: FT-1\nverdict: stop\n")

	var got []string
	for _, v := range archive.JudgeFiles(root, "FT-1") {
		got = append(got, filepath.Base(v.Path)+" "+v.Judged)
	}

	if want := []string{"9.md plan", "10.md change"}; !slices.Equal(got, want) {
		t.Errorf("judgeFiles = %v, want %v", got, want)
	}
}
