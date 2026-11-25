package drivectl

import (
	"errors"
	"slices"

	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/textx"
)

// The rows a finished task ends on: finished by the drive or outside it, or
// found finished when the drive started.
const (
	RowFinished        = "finished"
	RowAlreadyFinished = "already finished"
)

func RowDone(t drivestore.Task) bool {
	return t.Outcome != nil && (t.Outcome.Row == RowFinished || t.Outcome.Row == RowAlreadyFinished)
}

// A BaseStanding is what is known of one base of a join. Every reader of a
// join fills one from its own reads, and Through is the one rule over them.
type BaseStanding struct {
	Finished bool // its code is in the default branch: this batch finished it, or it was finished before
	Listed   bool // the batch holds it, so the batch itself moves it on
	Opened   bool // a listed base has its worktree and its agent
	Planned  bool // its lead's plan is saved
}

// Through reports a base its join no longer waits for. A join waits for
// merges. Under --plan it waits for saved plans instead: a listed base must
// also have opened, because a dependent opened first finds no base branch,
// and a base outside the batch has nothing here to open it.
func (b BaseStanding) Through(planOnly bool) bool {
	switch {
	case b.Finished:
		return true
	case !planOnly:
		return false
	case b.Listed:
		return b.Opened && b.Planned
	}

	return b.Planned
}

// JoinWaits is a join's bases its batch's record has not seen through, nil
// for a task that is no join. A base outside the record was through when the
// batch started, since a join refuses any other. planned is non-nil for a
// --plan batch, whose join waits for saved plans, not merges.
func JoinWaits(t drivestore.Task, tasks []drivestore.Task, planned func(string) bool) []string {
	var out []string

	for _, id := range t.Join {
		i := drivestore.HolderOf(tasks, id)
		if i < 0 {
			continue
		}

		b := BaseStanding{
			Finished: RowDone(tasks[i]), Listed: true, Opened: opened(tasks[i]),
			Planned: planned != nil && planned(id),
		}
		if !b.Through(planned != nil) {
			out = append(out, id)
		}
	}

	return out
}

// opened reads a record's task as opened: its mark, or for a record written
// before the mark, a skill in flight or a phase only an opened task reaches.
func opened(t drivestore.Task) bool {
	switch t.Phase {
	case drivestore.PhaseRunning, drivestore.PhaseVerifying, drivestore.PhaseRetrying,
		drivestore.PhaseParked, drivestore.PhaseWaiting:
		return true
	}

	return t.Opened || t.Skill != ""
}

// PlanOnly reports a batch started with --plan: its record's field, or its
// command line for a record written before the field.
func PlanOnly(rec *drivestore.Record) bool {
	return rec != nil && (rec.PlanOnly || slices.Contains(rec.Args, "--plan"))
}

// JoinPlanned is what JoinWaits takes for a batch. Under --plan it reports a
// base's plan saved under root. Otherwise it is nil, as the join waits for merges.
func JoinPlanned(planOnly bool, root string) func(string) bool {
	if !planOnly {
		return nil
	}

	return storage.Root(root).PlanLive
}

// JoinWhat is what a join's bases still owe it: a merge, or a plan.
func JoinWhat(planOnly bool) string {
	if planOnly {
		return "save a plan"
	}

	return "merge"
}

// JoinWaitPhrase is what a waiting join is said to wait for, after the word
// waiting: "for A and B to merge". The ids come as the caller shows them.
func JoinWaitPhrase(waits []string, planOnly bool) string {
	return "for " + textx.JoinAnd(waits) + " to " + JoinWhat(planOnly)
}

func SetFinished(t *drivestore.Task) {
	t.Skill, t.Queued, t.Phase, t.Outcome = "", false, drivestore.PhaseEnded, &drivestore.Outcome{Row: RowFinished}
}

// MarkFinished moves drive n's open rows that match to finished, under the
// drive's own lease: a held one is a running drive, which owns its record,
// so nothing is written. A row only ever moves to finished, since only a
// rerun knows a drive's next step for a halt settled halfway.
func MarkFinished(root string, n int, match func(drivestore.Task) bool) error {
	l, err := drivestore.Acquire(root, drivestore.DriveKey(n), 0)
	if held := (drivestore.HeldError{}); errors.As(err, &held) {
		return nil
	}

	if err != nil {
		return err
	}
	defer l.Release()

	_, err = drivestore.Update(root, n, func(rec *drivestore.Record) {
		for i := range rec.Tasks {
			if t := &rec.Tasks[i]; !RowDone(*t) && match(*t) {
				SetFinished(t)
			}
		}
	})

	return err
}
