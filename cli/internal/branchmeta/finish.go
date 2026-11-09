package branchmeta

import "strings"

// The states a DriveFinish records.
const (
	FinishQueued  = "queued"
	FinishRunning = "running"
)

// A DriveFinish is the Finish key's value.
type DriveFinish struct {
	State  string // FinishQueued or FinishRunning
	Action string // "" for none
}

// Format is the value the Finish key holds: `queued`, `running: merging #204`.
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

// ParseDriveFinish reads the Finish key's value; false for one that is empty
// or malformed.
func ParseDriveFinish(s string) (DriveFinish, bool) {
	st, action, _ := strings.Cut(strings.TrimSpace(s), " ")
	st = strings.TrimSuffix(st, ":")

	if st != FinishQueued && st != FinishRunning {
		return DriveFinish{}, false
	}

	return DriveFinish{State: st, Action: strings.TrimSpace(action)}, true
}
