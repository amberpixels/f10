//go:build unix

package ref

import "syscall"

// processAlive probes pid with signal 0. Drive runs as the user who reads
// its record, so a pid only another user may signal (EPERM) is a reused one.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
