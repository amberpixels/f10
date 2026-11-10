//go:build unix

package shell

import (
	"os/exec"
	"syscall"
)

// Detached starts exe in its own session and lets it go: it outlives
// the hook that starts it.
func Detached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...) //nolint:noctx // this binary, outliving its caller by design
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	return cmd.Process.Release()
}
