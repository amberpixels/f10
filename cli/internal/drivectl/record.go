package drivectl

import (
	"errors"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/storage"
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

// JoinWaits is a join's bases its batch's record has not seen finish, nil
// for a task that is no join. A base outside the record was finished when
// the batch started, since a join refuses any other. planned is non-nil for
// a --plan batch, whose join waits for saved plans, not merges.
func JoinWaits(t drivestore.Task, tasks []drivestore.Task, planned func(string) bool) []string {
	var out []string

	for _, id := range t.Join {
		i := slices.IndexFunc(tasks, func(o drivestore.Task) bool {
			lead, _, _ := strings.Cut(o.ID, ref.Separator)
			lead, _ = ref.SplitSuffix(lead)

			return strings.EqualFold(lead, id)
		})
		if i >= 0 && !RowDone(tasks[i]) && (planned == nil || !planned(id)) {
			out = append(out, id)
		}
	}

	return out
}

// PlanOnly reports a batch started with --plan, from its command line.
func PlanOnly(args []string) bool { return slices.Contains(args, "--plan") }

// JoinPlanned is what JoinWaits takes for a batch: under --plan, whether a
// base's plan is saved under root; else nil, since the join waits for merges.
func JoinPlanned(args []string, root string) func(string) bool {
	if !PlanOnly(args) {
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
