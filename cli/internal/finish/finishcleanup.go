package finish

import (
	"context"
	"fmt"
	"io"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/render"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// cleanup archives the task's files, prints the report, then removes what
// the task's checkout place holds. The report goes out before anything
// local does, and the workspace closes last.
func cleanup(ctx context.Context, w io.Writer, in FinishInput, d finishDecision, l landing, errs *cleanupErrs) error {
	in.step("archiving the plan")

	a, err := archiveTask(in.Storage, in.Task.ID, l.end, errs.check)
	if err != nil {
		return err
	}

	if a.planFailed {
		// a branch gone with its plan live reads as no task to a rerun
		errs.keep = d.stays() + ", since the plan is not archived"
	}

	notes := AppendNote(l.notes, a.missing)
	notes = AppendNote(notes, a.images)
	notes = AppendNote(notes, seriesLore(&in, l.end))

	rows := l.rows

	if a.plan != "" {
		rows = append(rows, render.Fact{Label: "plan", Value: a.plan, Path: true})
	}

	if a.handoff != "" && l.hand != nil {
		rows = append(rows, render.Fact{Label: "handoff", Value: a.handoff, Path: true})
	}

	if a.reviews != "" {
		rows = append(rows, render.Fact{Label: "reviews", Value: a.reviews})
	}

	// the report is copied out before the worktree goes and moved only after
	// it, so a rerun after a failed removal still finds the spike
	var spikeDst string

	early := d.spike != "" && d.place == inWorktree && gitx.Inside(d.spike, d.path)

	if d.spike != "" {
		spikeDst, err = spikeArchiveName(in.Storage, in.Task.ID)
		if err == nil && early {
			err = copySpike(d.spike, spikeDst)
		}

		if err := errs.check("archiving the spike report", err); err != nil {
			return err
		}

		if err != nil {
			// removing the worktree would delete the report's only copy
			errs.keep = "the worktree and branch stay, since the spike report is not archived"
		} else {
			rows = append(rows, render.Fact{Label: "spike", Value: spikeDst, Path: true})
		}
	}

	archiveLate := func() error {
		if d.spike == "" || early || spikeDst == "" {
			return nil
		}

		return archiveSpike(d.spike, spikeDst)
	}

	switch d.place {
	case inMainCheckout:
		notes = append(notes, fmt.Sprintf("%s is checked out in the main checkout at %s: after this report "+
			"main switches to %s and the branch is deleted; no worktree removed, no workspace closed",
			d.name, in.Main, d.def))
		rows = append(rows, render.Fact{Label: "path", Value: d.path})
	case noWorktree:
		keepForRerun(errs, d)

		if errs.keep != "" {
			notes = append(notes, d.name+" had no worktree left, and "+errs.keep)
		} else {
			notes = append(notes, d.name+" had no worktree left: only the branch is deleted, no workspace closed")
		}
	default:
		if d.ws != nil {
			rows = append(rows, render.Fact{Label: "workspace", Value: d.ws.ID})
		} else {
			notes = append(
				notes,
				"no "+worktree.Agents(in.Main).Name()+" workspace shows the worktree, so nothing to close",
			)
		}

		rows = append(rows, render.Fact{Label: "path", Value: d.path})
	}

	if d.self {
		notes = append(notes, "this workspace closes after this report")
	}

	if err := render.WriteReport(w, rows, notes); err != nil {
		return err
	}

	if err := taskcmd.WriteHandoff(w, l.hand); err != nil {
		return err
	}

	if err := errs.check("", taskcmd.MoveHandoff(a.handoffSrc, a.handoff)); err != nil {
		return fmt.Errorf("%s, but %w", l.ended, err)
	}

	if in.Handoff != nil && l.hand != nil {
		in.Handoff(l.hand)
	}

	keepForRerun(errs, d)

	if errs.keep != "" {
		return errs.done(in.Task.ID, l.ended)
	}

	switch d.place {
	case inMainCheckout:
		if err := errs.check("", switchMain(ctx, in.Main, d.def, d.name, l.kept)); err != nil {
			return fmt.Errorf("%s, but %w", l.ended, err)
		}

		errs.removed = true

		if err := errs.check("archiving the spike report", archiveLate()); err != nil {
			return fmt.Errorf("%s, but archiving the spike report failed: %w", l.ended, err)
		}

		finishRecords(w, in, d.name)
		in.sweepNow(ctx)

		return errs.done(in.Task.ID, l.ended)
	case noWorktree:
		in.step("deleting " + d.name)

		_ = errs.check("deleting branch "+d.name, gitx.DeleteBranch(ctx, in.Main, d.name))

		finishRecords(w, in, d.name)
		in.sweepNow(ctx)

		return errs.done(in.Task.ID, l.ended)
	case inWorktree:
	}

	return removeWorktree(ctx, w, in, d, l, errs, archiveLate)
}

// keepForRerun keeps the branch after a failed step: a rerun finds the task
// only by it.
func keepForRerun(errs *cleanupErrs, d finishDecision) {
	if errs.keep == "" && len(errs.failed) > 0 {
		errs.keep = d.stays() + " for the rerun"
	}
}

// removeWorktree is the last of a worktree's cleanup: removing the checkout
// deletes the branch, and the record with it, and closing the workspace may
// end this process.
func removeWorktree(ctx context.Context, w io.Writer, in FinishInput, d finishDecision, l landing,
	errs *cleanupErrs, archiveLate func() error,
) error {
	in.step("removing worktree")

	err := removeTaskCheckout(ctx, in, d.path, d.name, l.kept, d.dirty, d.kind == landingSpike)
	if err := errs.check("removing the worktree", err); err != nil {
		return err
	}

	errs.removed = err == nil

	if err := errs.check("archiving the spike report", archiveLate()); err != nil {
		return fmt.Errorf("%s, but archiving the spike report failed: %w", l.ended, err)
	}

	// before the close, which ends the process on the self-close path
	finishRecords(w, in, d.name)
	in.sweepNow(ctx)

	// closing its own workspace would end this process before the error prints
	if d.self && len(errs.failed) > 0 {
		errs.keep = "this workspace stays open"

		return errs.done(in.Task.ID, l.ended)
	}

	if d.self && d.mainWS != nil {
		err := worktree.Agents(in.Main).Focus(ctx, d.mainWS.ID)
		if err := errs.check("focusing the main workspace", err); err != nil {
			return err
		}
	}

	if d.ws != nil {
		err := worktree.Agents(in.Main).Close(ctx, d.ws.ID)
		if err := errs.check("closing workspace "+d.ws.ID, err); err != nil {
			return err
		}
	}

	return errs.done(in.Task.ID, l.ended)
}
