package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// A goneError is a checkout a finish that stopped halfway left: no branch,
// or a branch with no worktree.
type goneError struct {
	err    error
	branch string // "" for no branch
}

func (e goneError) Error() string { return e.err.Error() }

func (e goneError) Unwrap() error { return e.err }

// finishBranchless ends a task with no branch here: already finished when
// its plan is archived, else a parent its subtasks delivered.
func finishBranchless(ctx context.Context, w io.Writer, in finishInput, gone goneError) error {
	if !in.storage.PlanArchived(in.task.ID) {
		return finishParent(ctx, w, in, gone)
	}

	if err := writeReport(w, []fact{taskFact(ctx, w, in.main, in.task.Label())},
		[]string{in.task.ID + " has no branch and its plan is archived: already finished"}); err != nil {
		return err
	}

	finishRecords(w, in, in.branch)

	return nil
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
		x, err := loadBranchIndex(ctx, in.main)
		if err != nil {
			return err
		}

		branch, err := x.any(ctx, k.ID)
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
