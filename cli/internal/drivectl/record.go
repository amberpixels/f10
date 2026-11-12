package drivectl

import (
	"errors"

	"github.com/amberpixels/f10/cli/internal/drivestore"
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
