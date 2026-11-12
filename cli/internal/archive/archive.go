// Package archive keeps what a finished task leaves behind: archived plans and
// findings, the review and verdict series, and the handoff file.
package archive

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// ArchiveReviews moves a findings directory to reviews/archive/<id>.<N>/,
// so the task's next PR starts its own series of rounds. N always counts
// from 1, since a bare <id>/ under archive would read as a live directory.
func ArchiveReviews(dir string, end SeriesEnd) (string, error) {
	if !fsx.Exists(dir) {
		return "", nil
	}

	archived := storage.ArchiveOf(filepath.Dir(dir))
	if err := os.MkdirAll(archived, 0o750); err != nil {
		return "", fmt.Errorf("archiving the findings at %s: %w", dir, err)
	}

	target := NextSeries(archived, filepath.Base(dir))
	if err := os.Rename(dir, target); err != nil {
		return "", fmt.Errorf("archiving the findings at %s: %w", dir, err)
	}

	return target, end.Write(target)
}

// FreeArchiveName numbers `<id>.<N>.md` the way steps/plan.md does.
func FreeArchiveName(dir, id string) string {
	candidate := filepath.Join(dir, id+".md")
	if !fsx.Exists(candidate) {
		return candidate
	}

	for n := 1; ; n++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s.%d.md", id, n))
		if !fsx.Exists(candidate) {
			return candidate
		}
	}
}

// NewestArchived is the most recently written of `<id>.md` and `<id>.<N>.md`
// in an archive dir, "" for none. FreeArchiveName takes the lowest free
// name, so the number does not order them and the file's time does.
func NewestArchived(dir, id string) string {
	paths, _ := filepath.Glob(filepath.Join(dir, id+".*.md"))
	if one := filepath.Join(dir, id+".md"); fsx.Exists(one) {
		paths = append(paths, one)
	}

	newest := ""
	for _, p := range paths {
		if newest == "" || ModTime(p).After(ModTime(newest)) {
			newest = p
		}
	}

	return newest
}

// ArchivePlan moves the plan into the shared archive. A missing plan is a
// note, since a task shipped without one still needs cleaning up; one that
// cannot move is an error, since removing the worktree would delete it.
func ArchivePlan(root storage.Root, id string) (string, string, error) {
	plan, dst := root.Plan(id), root.Archive()

	if !fsx.Exists(plan) {
		return "", fmt.Sprintf("no plan at %s, nothing archived", plan), nil
	}

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return "", "", fmt.Errorf("archiving the plan at %s: %w", plan, err)
	}

	target := FreeArchiveName(dst, id)
	if err := os.Rename(plan, target); err != nil {
		return "", "", fmt.Errorf("archiving the plan at %s: %w", plan, err)
	}

	return target, "", nil
}
