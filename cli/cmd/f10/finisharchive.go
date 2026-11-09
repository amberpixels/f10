package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// archivePlan moves the plan into the shared archive. A missing plan is a
// note, since a task shipped without one still needs cleaning up; one that
// cannot move is an error, since removing the worktree would delete it.
func archivePlan(worktree, main, plansDir, id string) (string, string, error) {
	plan := taskPlan(worktree, plansDir, id)
	dst := filepath.Join(main, ".f10", "plans", "archive")

	if plansDir != "" {
		dst = filepath.Join(plansDir, "archive")
	}

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

	archive := filepath.Join(filepath.Dir(dir), "archive")
	if err := os.MkdirAll(archive, 0o750); err != nil {
		return "", fmt.Errorf("archiving the findings at %s: %w", dir, err)
	}

	target := nextSeries(archive, filepath.Base(dir))
	if err := os.Rename(dir, target); err != nil {
		return "", fmt.Errorf("archiving the findings at %s: %w", dir, err)
	}

	return target, end.write(target)
}

// taskReviews is the task's findings directory, beside its plans.
func taskReviews(main, plansDir, id string) string {
	return filepath.Join(filepath.Dir(taskPlansDir(main, plansDir)), "reviews", id)
}

// removeImages drops the images `task read` downloaded for the task; a
// failure is a note, since the task is finished either way.
func removeImages(main, plansDir, id string) string {
	dir := filepath.Join(main, ".f10", "images", id)
	if plansDir != "" {
		dir = filepath.Join(filepath.Dir(plansDir), "images", id)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Sprintf("the task's images at %s stay: %v", dir, err)
	}

	return ""
}

func taskPlansDir(checkout, plansDir string) string {
	return cmp.Or(plansDir, filepath.Join(checkout, ".f10", "plans"))
}

// taskPlan is the task's plan in the shared plans dir, else in the
// checkout's own when there is no shared one.
func taskPlan(checkout, plansDir, id string) string {
	return filepath.Join(taskPlansDir(checkout, plansDir), id+".md")
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

func inside(dir, path string) bool {
	rel, err := filepath.Rel(gitx.Resolved(path), gitx.Resolved(dir))
	if err != nil {
		return false
	}

	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
