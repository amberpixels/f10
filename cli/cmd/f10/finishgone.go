package main

import (
	"context"
	"io"
	"path/filepath"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A goneError is a task whose checkout is already gone, as a finish that
// stopped halfway leaves it: no branch at all, or a branch with no
// worktree.
type goneError struct {
	err    error
	branch string // "" for no branch
}

func (e goneError) Error() string { return e.err.Error() }

func (e goneError) Unwrap() error { return e.err }

// finishGone finishes what a finish that stopped halfway left: a task with
// no branch whose plan is archived is already finished, and a branch with
// no worktree whose PR merged is cleaned up without one. Anything else
// keeps the refusal, which guards branches start never made.
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

	note, err := pullMain(ctx, in.main, def, p.sha())
	if err != nil {
		return err
	}

	notes = appendNote(notes, note)

	released, err := releaseDependents(ctx, in.main, name, def)
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

	rows := []fact{
		{label: "task", value: in.task.Label()},
		{label: "pr", value: p.url(), url: true},
		{label: "merge", value: p.sha()},
	}

	if archived != "" {
		rows = append(rows, fact{label: "plan", value: archived})
	}

	notes = append(notes, name+" had no worktree left: only the branch is deleted, no workspace closed")

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	in.step("deleting " + name)

	return gitx.DeleteBranch(ctx, in.main, name)
}

// planArchived reports the task's plan in the shared archive, as finish
// leaves it.
func planArchived(in finishInput) bool {
	dir := filepath.Join(in.main, ".f10", "plans", "archive")
	if in.plansDir != "" {
		dir = filepath.Join(in.plansDir, "archive")
	}

	if exists(filepath.Join(dir, in.task.ID+".md")) {
		return true
	}

	older, _ := filepath.Glob(filepath.Join(dir, in.task.ID+".*.md"))

	return len(older) > 0
}
