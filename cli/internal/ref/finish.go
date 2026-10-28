package ref

import (
	"strconv"
	"strings"
)

// FinishKey is `f10 drive`'s record, beside TaskKey, that it runs the
// branch's finish: its own pid, whether finish is queued or running, then
// what it is doing. Finish removes it with the branch; drive clears it on
// any other end, and readers ignore one whose process is gone.
const FinishKey = "f10-finish"

// The states a DriveFinish records.
const (
	FinishQueued  = "queued"
	FinishRunning = "running"
)

// A DriveFinish is FinishKey's value.
type DriveFinish struct {
	PID    int
	State  string // FinishQueued or FinishRunning
	Action string // "" for none
}

// alive reports whether a process runs; swapped in tests.
var alive = processAlive

// Format is the value FinishKey holds.
func (d DriveFinish) Format() string {
	return strings.TrimSpace(strconv.Itoa(d.PID) + " " + d.State + " " + d.Action)
}

// Live reports whether the drive that wrote the record still runs.
func (d DriveFinish) Live() bool {
	return d.PID > 0 && alive(d.PID)
}

// Running reports finish itself at work, not queued behind ship, a base
// or a catchup.
func (d DriveFinish) Running() bool {
	return d.State == FinishRunning
}

// ParseDriveFinish reads FinishKey's value; false for one that is empty
// or malformed.
func ParseDriveFinish(s string) (DriveFinish, bool) {
	fields := strings.SplitN(strings.TrimSpace(s), " ", 3)

	n, err := strconv.Atoi(fields[0])
	if err != nil || n <= 0 || len(fields) < 2 || (fields[1] != FinishQueued && fields[1] != FinishRunning) {
		return DriveFinish{}, false
	}

	d := DriveFinish{PID: n, State: fields[1]}
	if len(fields) == 3 {
		d.Action = strings.TrimSpace(fields[2])
	}

	return d, true
}

// LiveDriveFinish is the record FinishKey holds when its drive still runs.
func LiveDriveFinish(s string) (DriveFinish, bool) {
	d, ok := ParseDriveFinish(s)
	if !ok || !d.Live() {
		return DriveFinish{}, false
	}

	return d, true
}
