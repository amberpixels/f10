//go:build !unix

package shell

import "os/exec"

// killGroup leaves cmd as it is: only unix has process groups to kill.
func killGroup(*exec.Cmd) {}
