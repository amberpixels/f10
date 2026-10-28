//go:build unix

package ref

import (
	"errors"
	"syscall"
)

// processAlive probes pid with signal 0; EPERM is a process someone else owns.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)

	return err == nil || errors.Is(err, syscall.EPERM)
}
