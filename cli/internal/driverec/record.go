// Package driverec marks a task's row in its drive record and asks the watcher
// to take a project on.
package driverec

import (
	"errors"

	"github.com/amberpixels/f10/cli/internal/drives"
)

// The rows a finished task ends on: finished by the drive or outside it, or
// found finished when the drive started.
const (
	RowFinished        = "finished"
	RowAlreadyFinished = "already finished"
)

func RowDone(t drives.Task) bool {
	return t.Outcome != nil && (t.Outcome.Row == RowFinished || t.Outcome.Row == RowAlreadyFinished)
}

func SetFinished(t *drives.Task) {
	t.Skill, t.Queued, t.Phase, t.Outcome = "", false, drives.PhaseEnded, &drives.Outcome{Row: RowFinished}
}

// MarkFinished moves drive n's open rows that match to finished, under the
// drive's own lease: a held one is a running drive, which owns its record,
// so nothing is written. A row only ever moves to finished, since only a
// rerun knows a drive's next step for a halt settled halfway.
func MarkFinished(root string, n int, match func(drives.Task) bool) error {
	l, err := drives.Acquire(root, drives.DriveKey(n), 0)
	if held := (drives.HeldError{}); errors.As(err, &held) {
		return nil
	}

	if err != nil {
		return err
	}
	defer l.Release()

	_, err = drives.Update(root, n, func(rec *drives.Record) {
		for i := range rec.Tasks {
			if t := &rec.Tasks[i]; !RowDone(*t) && match(*t) {
				SetFinished(t)
			}
		}
	})

	return err
}
