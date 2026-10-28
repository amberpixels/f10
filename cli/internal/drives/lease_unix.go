//go:build unix

package drives

import (
	"errors"
	"os"
	"syscall"
)

var errHeld = errors.New("lock held")

func lockExclusive(f *os.File) error { return flock(f, syscall.LOCK_EX|syscall.LOCK_NB) }

func lockShared(f *os.File) error { return flock(f, syscall.LOCK_SH|syscall.LOCK_NB) }

func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

func flock(f *os.File, how int) error {
	err := syscall.Flock(int(f.Fd()), how)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errHeld
	}

	return err
}
