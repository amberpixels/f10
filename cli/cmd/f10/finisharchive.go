package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// taskArchive is what archiveTask moved aside, for the caller's report.
type taskArchive struct {
	plan       string // where the plan went, "" for none
	planFailed bool   // the plan is still live, so the branch a rerun finds it by must stay
	missing    string // the note for a task with no plan
	images     string // the note for images that stay
	reviews    string // where the findings went, "" for none
	// the handoff's source and archive target, "" for none; the caller moves
	// it, since finish prints it before the move
	handoffSrc, handoff string
}

// archiveTask is the cleanup every finished task gets, from finish and from
// the janitor. check rules on each failed step: a non-nil return stops here.
func archiveTask(root storage.Root, id string, end *seriesEnd,
	check func(step string, err error) error,
) (taskArchive, error) {
	var a taskArchive

	plan, note, err := archivePlan(root, id)
	if err := check("archiving the plan", err); err != nil {
		return a, err
	}

	a.plan, a.planFailed, a.missing = plan, err != nil, note

	// a plan still live keeps the images its text links to
	if !a.planFailed {
		a.images = removeImages(root, id)
	}

	a.handoffSrc, a.handoff = handoffTarget(root, id)

	if end != nil {
		a.reviews, err = closeSeries(root.Reviews(id), *end)
	} else {
		a.reviews, err = archiveReviews(root.Reviews(id), seriesEnd{})
	}

	if err := check("archiving the reviews", err); err != nil {
		return a, err
	}

	return a, nil
}

// archivePlan moves the plan into the shared archive. A missing plan is a
// note, since a task shipped without one still needs cleaning up; one that
// cannot move is an error, since removing the worktree would delete it.
func archivePlan(root storage.Root, id string) (string, string, error) {
	plan, dst := root.Plan(id), root.Archive()

	if !fsx.Exists(plan) {
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
	if !fsx.Exists(dir) {
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
