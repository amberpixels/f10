package ref

import (
	"strconv"
	"strings"
)

// FinishKey is `f10 drive`'s record, beside TaskKey, of where its finish
// of the branch stands: queued or running, then what it is doing. Finish
// removes it with the branch and drive clears it on any other end; whether
// the drive still runs is its task's lease, never this record.
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

// Running reports finish itself at work, not queued behind ship, a base
// or a catchup.
func (d DriveFinish) Running() bool {
	return d.State == FinishRunning
}

// ParseDriveFinish reads FinishKey's value; false for one that is empty
// or malformed. A leading pid, as an older drive wrote, is skipped.
func ParseDriveFinish(s string) (DriveFinish, bool) {
	s = strings.TrimSpace(s)

	if first, rest, ok := strings.Cut(s, " "); ok {
		if _, err := strconv.Atoi(first); err == nil {
			s = strings.TrimSpace(rest)
		}
	}

	st, action, _ := strings.Cut(s, " ")
	st = strings.TrimSuffix(st, ":")

	if st != FinishQueued && st != FinishRunning {
		return DriveFinish{}, false
	}

	return DriveFinish{State: st, Action: strings.TrimSpace(action)}, true
}
