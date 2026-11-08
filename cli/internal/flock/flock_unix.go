//go:build unix

package flock

import (
	"errors"
	"os"
	"syscall"
)

// Exclusive locks f without waiting.
func Exclusive(f *os.File) error { return lock(f, syscall.LOCK_EX|syscall.LOCK_NB) }

// Shared takes f's shared lock without waiting.
func Shared(f *os.File) error { return lock(f, syscall.LOCK_SH|syscall.LOCK_NB) }

// Unlock drops whichever lock f holds.
func Unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

func lock(f *os.File, how int) error {
	err := syscall.Flock(int(f.Fd()), how)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrHeld
	}

	return err
}
