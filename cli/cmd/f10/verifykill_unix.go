//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// killGroup runs cmd in a process group of its own and kills that group on
// cancel, so the children of `sh -c` die with it.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
