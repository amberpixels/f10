//go:build !unix

package main

import "os/exec"

// killGroup leaves cmd as it is: only unix has process groups to kill.
func killGroup(*exec.Cmd) {}
