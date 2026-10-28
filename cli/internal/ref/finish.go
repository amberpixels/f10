package ref

import (
	"strconv"
	"strings"
)

// FinishKey is `f10 drive`'s record, beside TaskKey, that it runs the
// branch's finish: its own pid, then what finish is doing, none while it
// is queued. Finish removes it with the branch; drive clears it on any
// other end, and readers ignore one whose process is gone.
const FinishKey = "f10-finish"

// A DriveFinish is FinishKey's value.
type DriveFinish struct {
	PID    int
	Action string // "" while queued
}

// alive reports whether a process runs; swapped in tests.
var alive = processAlive

// Format is the value FinishKey holds.
func (d DriveFinish) Format() string {
	if d.Action == "" {
		return strconv.Itoa(d.PID)
	}

	return strconv.Itoa(d.PID) + " " + d.Action
}

// Live reports whether the drive that wrote the record still runs.
func (d DriveFinish) Live() bool {
	return d.PID > 0 && alive(d.PID)
}

// Waiting reports a record whose drive holds finish back for a base.
func (d DriveFinish) Waiting() bool {
	return strings.HasPrefix(d.Action, "waiting")
}

// ParseDriveFinish reads FinishKey's value; false for one that is empty
// or malformed.
func ParseDriveFinish(s string) (DriveFinish, bool) {
	pid, action, _ := strings.Cut(strings.TrimSpace(s), " ")

	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return DriveFinish{}, false
	}

	return DriveFinish{PID: n, Action: strings.TrimSpace(action)}, true
}

// LiveDriveFinish is the record FinishKey holds when its drive still runs.
func LiveDriveFinish(s string) (DriveFinish, bool) {
	d, ok := ParseDriveFinish(s)
	if !ok || !d.Live() {
		return DriveFinish{}, false
	}

	return d, true
}
