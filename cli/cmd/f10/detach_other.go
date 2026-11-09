//go:build !unix

package main

import (
	"os/exec"
)

// startDetached starts exe and lets it go.
func startDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...) //nolint:noctx // this binary, outliving its caller by design
	if err := cmd.Start(); err != nil {
		return err
	}

	return cmd.Process.Release()
}
