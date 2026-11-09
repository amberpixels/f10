package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/storage"
)

// archivePlan moves the plan into the shared archive. A missing plan is a
// note, since a task shipped without one still needs cleaning up; one that
// cannot move is an error, since removing the worktree would delete it.
func archivePlan(root storage.Root, id string) (string, string, error) {
	plan, dst := root.Plan(id), root.Archive()

	if !exists(plan) {
		return "", fmt.Sprintf("no plan at %s, nothing archived", plan), nil
	}

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return "", "", fmt.Errorf("archiving the plan at %s: %w", plan, err)
	}

	target := freeArchiveName(dst, id)
	if err := os.Rename(plan, target); err != nil {
		return "", "", fmt.Errorf("archiving the plan at %s: %w", plan, err)
	}

	return target, "", nil
}

// archiveReviews moves a findings directory to reviews/archive/<id>.<N>/,
// so the task's next PR starts its own series of rounds. N always counts
// from 1, since a bare <id>/ under archive would read as a live directory.
func archiveReviews(dir string, end seriesEnd) (string, error) {
	if !exists(dir) {
		return "", nil
	}

	archive := storage.ArchiveOf(filepath.Dir(dir))
	if err := os.MkdirAll(archive, 0o750); err != nil {
		return "", fmt.Errorf("archiving the findings at %s: %w", dir, err)
	}

	target := nextSeries(archive, filepath.Base(dir))
	if err := os.Rename(dir, target); err != nil {
		return "", fmt.Errorf("archiving the findings at %s: %w", dir, err)
	}

	return target, end.write(target)
}

// removeImages drops the images `task read` downloaded for the task; a
// failure is a note, since the task is finished either way.
func removeImages(root storage.Root, id string) string {
	dir := root.Images(id)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Sprintf("the task's images at %s stay: %v", dir, err)
	}

	return ""
}

// freeArchiveName numbers `<id>.<N>.md` the way steps/plan.md does.
func freeArchiveName(dir, id string) string {
	candidate := filepath.Join(dir, id+".md")
	if !exists(candidate) {
		return candidate
	}

	for n := 1; ; n++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s.%d.md", id, n))
		if !exists(candidate) {
			return candidate
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
