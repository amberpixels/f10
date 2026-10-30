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

		return writeReport(w, []fact{{label: "task", value: in.task.Label()}},
			[]string{in.task.ID + " has no branch and its plan is archived: already finished"})
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

	reviews, err := archiveReviews(taskReviews(in.main, in.plansDir, in.task.ID))
	if err != nil {
		return err
	}

	rows := []fact{
		{label: "task", value: in.task.Label()},
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

	return gitx.DeleteBranch(ctx, in.main, name)
}

// planArchived wants an archived plan with no live one beside it, since a
// re-plan archives the old plan too.
func planArchived(in finishInput) bool {
	plans := filepath.Join(in.main, ".f10", "plans")
	if in.plansDir != "" {
		plans = in.plansDir
	}

	if exists(filepath.Join(plans, in.task.ID+".md")) {
		return false
	}

	dir := filepath.Join(plans, "archive")
	if exists(filepath.Join(dir, in.task.ID+".md")) {
		return true
	}

	older, _ := filepath.Glob(filepath.Join(dir, in.task.ID+".*.md"))

	return len(older) > 0
}
