package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

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
			return finishParent(ctx, w, in, gone)
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

	// the PR already merged; the local branch still holds the tip the handoff was written at
	hand := handoffLines(taskHandoff(in.main, in.plansDir, in.task.ID), in.task.ID, branchTip(ctx, in.main, name, p))

	p, notes, err := landPull(ctx, in, name, def, p)
	if err != nil {
		return err
	}

	notes = append(notes, riderNotes...)

	in.step("pulling " + def)

	// the merge has landed: each step from here runs on past a failure
	after := &afterMerge{on: true}

	note, deferred := pullMain(ctx, in.root, in.main, def, p.sha())
	notes = appendNote(notes, note)

	if deferred && in.deferred != nil {
		in.deferred(note)
	}

	released, err := releaseDependents(ctx, in.main, name, def, true)
	_ = after.check("releasing dependents", err)

	notes = append(notes, released...)

	in.step("archiving the plan")

	archived, note, err := archivePlan(in.main, in.main, in.plansDir, in.task.ID)
	_ = after.check("archiving the plan", err)
	if err != nil {
		after.keep = "the branch stays, since the plan is not archived"
	}

	notes = appendNote(notes, note)
	notes = appendNote(notes, removeImages(in.main, in.plansDir, in.task.ID))

	handoffSrc, handed := handoffTarget(in.main, in.plansDir, in.task.ID)

	reviews, err := closeSeries(taskReviews(in.main, in.plansDir, in.task.ID),
		seriesEnd{pr: pullNumber(p.url()), head: p.sha()})
	_ = after.check("archiving the reviews", err)

	rows := []fact{
		taskFact(ctx, w, in.main, in.task.Label()),
		{label: "pr", value: p.url(), url: true},
		{label: "merge", value: p.sha()},
	}

	if archived != "" {
		rows = append(rows, fact{label: "plan", value: archived, path: true})
	}

	if handed != "" {
		rows = append(rows, fact{label: "handoff", value: handed, path: true})
	}

	if reviews != "" {
		rows = append(rows, fact{label: "reviews", value: reviews})
	}

	// a rerun finds the task by its branch, so a step that failed keeps it
	if after.keep == "" && len(after.failed) > 0 {
		after.keep = "the branch stays for the rerun"
	}

	if after.keep != "" {
		notes = append(notes, name+" had no worktree left, and "+after.keep)
	} else {
		notes = append(notes, name+" had no worktree left: only the branch is deleted, no workspace closed")
	}

	if err := writeReport(w, rows, notes); err != nil {
		return err
	}

	if err := writeHandoff(w, hand); err != nil {
		return err
	}

	_ = after.check("", moveHandoff(handoffSrc, handed))

	if in.handoff != nil {
		in.handoff(hand)
	}

	ended := p.url() + " merged"

	if after.keep == "" && len(after.failed) > 0 {
		after.keep = "the branch stays for the rerun"
	}

	if after.keep != "" {
		return after.done(in.task.ID, ended)
	}

	in.step("deleting " + name)

	_ = after.check("deleting branch "+name, gitx.DeleteBranch(ctx, in.main, name))

	finishRecords(w, in, name)

	return after.done(in.task.ID, ended)
}

// finishParent closes a parent its subtasks delivered: it never had a branch
// or a plan of its own, so every subtask closed and branchless is the proof.
// A task with no subtasks keeps the refusal.
func finishParent(ctx context.Context, w io.Writer, in finishInput, gone goneError) error {
	if in.subtasks == nil {
		return gone.err
	}

	kids, err := in.subtasks(ctx, in.task)
	if errors.Is(err, errNoParentLink) || (err == nil && len(kids) == 0) {
		return gone.err
	}

	if err != nil {
		return fmt.Errorf("%s: reading its subtasks: %w", in.task.ID, err)
	}

	var unfinished, ids []string

	for _, k := range kids {
		ids = append(ids, k.ID)

		// drive counts an open subtask with its plan archived as finished, so
		// the refusal says only the tracker disagrees
		if !strings.EqualFold(k.State, "closed") {
			reason := k.ID + " is open"
			if k.Finished {
				reason += " in the tracker though its plan is archived"
			}

			unfinished = append(unfinished, reason)

			continue
		}

		// a tracking ref the host deleted but this clone never pruned counts too,
		// so the ref is named for the user to prune
		branch, err := anyTaskBranch(ctx, in.main, k.ID)
		if err != nil {
			return err
		}

		if branch != "" {
			unfinished = append(unfinished, k.ID+" still has branch "+branch)
		}
	}

	if len(unfinished) > 0 {
		return fmt.Errorf("%s has subtasks not finished: %s; it stays open", in.task.ID, strings.Join(unfinished, ", "))
	}

	note := "closed: delivered by " + strings.Join(ids, ", ")

	if in.closeTask == nil {
		note = "every subtask is done (" + strings.Join(ids, ", ") + "), and the tracker is not the host: close " +
			in.task.ID + " there"
	} else {
		in.step("closing " + in.task.ID)

		if err := in.closeTask(ctx, in.task); err != nil {
			return fmt.Errorf("closing %s: %w", in.task.ID, err)
		}
	}

	if err := writeReport(w, []fact{taskFact(ctx, w, in.main, in.task.Label())}, []string{note}); err != nil {
		return err
	}

	finishRecords(w, in, in.branch)

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
