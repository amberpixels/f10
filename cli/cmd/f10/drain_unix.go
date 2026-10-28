//go:build unix

package main

import "syscall"

// drainSignal is what the daemon sends a drive to stop at its next tick.
const drainSignal = syscall.SIGUSR1
