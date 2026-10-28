//go:build unix

package main

import (
	"os/exec"
	"syscall"
	"time"
)

// startDetached starts exe in its own session and lets it go: it outlives
// the hook that starts it.
func startDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...) //nolint:noctx // this binary, outliving its caller by design
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	return cmd.Process.Release()
}

// waitGone waits, up to limit, for process pid to end, then a moment more
// for its parent to reap it.
func waitGone(pid int, limit time.Duration) {
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); {
		if syscall.Kill(pid, 0) != nil {
			break
		}

		time.Sleep(200 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)
}
