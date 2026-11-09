package ref

import "strings"

// FinishKey is `f10 drive`'s record, beside TaskKey, that it finishes the
// branch, and where that finish stands. Whether the drive still runs is its
// task's lease, never this record: finish removes it with the branch, and a
// drive that stops short sets it back to queued.
const FinishKey = "f10-finish"

// The states a DriveFinish records.
const (
	FinishQueued  = "queued"
	FinishRunning = "running"
)

// A DriveFinish is FinishKey's value.
type DriveFinish struct {
	State  string // FinishQueued or FinishRunning
	Action string // "" for none
}

// Format is the value FinishKey holds: `queued`, `running: merging #204`.
func (d DriveFinish) Format() string {
	if d.Action == "" {
		return d.State
	}

	return d.State + ": " + d.Action
}

// Running reports finish itself at work, not queued behind ship or a base.
func (d DriveFinish) Running() bool {
	return d.State == FinishRunning
}

// ParseDriveFinish reads FinishKey's value; false for one that is empty
// or malformed.
func ParseDriveFinish(s string) (DriveFinish, bool) {
	st, action, _ := strings.Cut(strings.TrimSpace(s), " ")
	st = strings.TrimSuffix(st, ":")

	if st != FinishQueued && st != FinishRunning {
		return DriveFinish{}, false
	}

	return DriveFinish{State: st, Action: strings.TrimSpace(action)}, true
}
