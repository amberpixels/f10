package finish

import (
	"fmt"
	"os"

	"github.com/amberpixels/f10/cli/internal/archive"
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
func archiveTask(root storage.Root, id string, end *archive.SeriesEnd,
	check func(step string, err error) error,
) (taskArchive, error) {
	var a taskArchive

	plan, note, err := archive.ArchivePlan(root, id)
	if err := check("archiving the plan", err); err != nil {
		return a, err
	}

	a.plan, a.planFailed, a.missing = plan, err != nil, note

	// a plan still live keeps the images its text links to
	if !a.planFailed {
		a.images = removeImages(root, id)
	}

	a.handoffSrc, a.handoff = archive.HandoffTarget(root, id)

	if end != nil {
		a.reviews, err = CloseSeries(root.Reviews(id), *end)
	} else {
		a.reviews, err = archive.ArchiveReviews(root.Reviews(id), archive.SeriesEnd{})
	}

	if err := check("archiving the reviews", err); err != nil {
		return a, err
	}

	return a, nil
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
