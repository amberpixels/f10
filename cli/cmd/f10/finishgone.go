package main

import (
	"context"
	"io"
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A goneError is a checkout a finish that stopped halfway left: no branch,
// or a branch with no worktree.
type goneError struct {
	err    error
	branch string // "" for no branch
}

func (e goneError) Error() string { return e.err.Error() }

func (e goneError) Unwrap() error { return e.err }

// finishGone completes a finish that stopped halfway; anything else keeps
// the refusal, which guards branches start never made.
func finishGone(ctx context.Context, w io.Writer, in finishInput, gone goneError) error {
	if gone.branch == "" {
		if !planArchived(in) {
			return gone.err
		}

		if err := writeReport(w, []fact{taskFact(ctx, w, in.main, in.task.Label())},
			[]string{in.task.ID + " has no branch and its plan is archived: already finished"}); err != nil {
			return err
		}

		finishRecords(w, in, in.branch)

		return nil
	}

	name := gone.branch

	p, err := in.host.pull(ctx, name)
	if err != nil || p.status() != pullMerged {
		return gone.err
	}

	def, err := localDefault(ctx, in.main)
	if err != nil {
		return err
	}

	var riderNotes []string

	if len(in.task.Riders()) > 0 {
		if in.close == nil {
			return ridersError{task: in.task}
		}

		if riderNotes, err = settleRiders(ctx, in, name, true); err != nil {
			return err
		}
	}

	p, notes, err := landPull(ctx, in, name, def, p)
	if err != nil {
		return err
	}

	notes = append(notes, riderNotes...)

	in.step("pulling " + def)

	note, err := pullMain(ctx, in.root, in.main, def, p.sha())
	if err != nil {
		return err
	}

	notes = appendNote(notes, note)

	released, err := releaseDependents(ctx, in.main, name, def, true)
	if err != nil {
		return err
	}

	notes = append(notes, released...)

	in.step("archiving the plan")

	archived, note, err := archivePlan(in.main, in.main, in.plansDir, in.task.ID)
	if err != nil {
		return err
	}

	notes = appendNote(notes, note)
	notes = appendNote(notes, removeImages(in.main, in.plansDir, in.task.ID))

	reviews, err := closeSeries(taskReviews(in.main, in.plansDir, in.task.ID),
		seriesEnd{pr: pullNumber(p.url()), head: p.sha()})
	if err != nil {
		return err
	}

	rows := []fact{
		taskFact(ctx, w, in.main, in.task.Label()),
		{label: "pr", value: p.url(), url: true},
		{label: "merge", value: p.sha()},
	}

	if archived != "" {
		rows = append(rows, fact{label: "plan", value: archived})
	}

	if reviews != "" {
		rows = append(rows, fact{label: "reviews", value: reviews})
	}

	notes = append(notes, name+" had no worktree left: only the branch is deleted, no workspace closed")

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	in.step("deleting " + name)

	if err := gitx.DeleteBranch(ctx, in.main, name); err != nil {
		return err
	}

	finishRecords(w, in, name)

	return nil
}

// planArchived wants an archived plan with no live one beside it, since a
// re-plan archives the old plan too.
func planArchived(in finishInput) bool {
	plans := filepath.Join(in.main, ".f10", "plans")
	if in.plansDir != "" {
		plans = in.plansDir
	}

	return planArchivedIn(plans, in.task.ID)
}

func planArchivedIn(plans, id string) bool {
	if exists(filepath.Join(plans, id+".md")) {
		return false
	}

	dir := filepath.Join(plans, "archive")
	if exists(filepath.Join(dir, id+".md")) {
		return true
	}

	older, _ := filepath.Glob(filepath.Join(dir, id+".*.md"))

	return len(older) > 0
}

// taskFinished is finish's own "already finished", which drive list shares so
// the two never disagree: no task branch here or on origin, and the plan
// archived. A git error reads as unfinished, since a wrong yes hides a halt.
func taskFinished(ctx context.Context, main, plans, id string) bool {
	if !planArchivedIn(plans, id) {
		return false
	}

	branched, err := hasTaskBranch(ctx, main, id)

	return err == nil && !branched
}
