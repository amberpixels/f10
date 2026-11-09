package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/flock"
)

// The daemon's locks in its run dir: the running daemon's, and the one a
// client holds while it starts one.
const (
	lockDaemon = "daemon"
	lockStart  = "start"
)

// lockPath is a daemon lock's file. It keeps the path the drive leases use,
// because an older daemon still running holds its lock there.
func lockPath(dir, name string) string {
	return filepath.Join(dir, "drives", "locks", name+".lock")
}

func lock(dir, name string) (*flock.Lease, error) {
	return flock.Acquire(lockPath(dir, name), "pid="+strconv.Itoa(os.Getpid()))
}

// daemonHeld reports a daemon running: its lock is held.
func daemonHeld(dir string) bool {
	_, h := flock.Probe(lockPath(dir, lockDaemon))

	return h
}

// holderPID is the pid a refused lock's holder wrote, 0 when unreadable.
func holderPID(err flock.HeldError) int {
	for field := range strings.FieldsSeq(err.Holder) {
		if v, ok := strings.CutPrefix(field, "pid="); ok {
			n, _ := strconv.Atoi(v)

			return n
		}
	}

	return 0
}
