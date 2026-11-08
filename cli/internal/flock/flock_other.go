//go:build !unix

package flock

import "os"

// No flock here: every lock is taken and none reads held.

// Exclusive locks f without waiting.
func Exclusive(*os.File) error { return nil }

// Shared takes f's shared lock without waiting.
func Shared(*os.File) error { return nil }

// Unlock drops whichever lock f holds.
func Unlock(*os.File) error { return nil }
