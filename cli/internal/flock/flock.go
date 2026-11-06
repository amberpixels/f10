// Package flock holds advisory file locks, which the kernel drops with the
// holder's process, so a crashed holder never strands one.
package flock

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrHeld is a lock another holder has.
var ErrHeld = errors.New("lock held")

const retryDelay = 10 * time.Millisecond

// Wait takes path's exclusive lock, retrying until wait runs out; the
// returned func releases it.
func Wait(path string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	deadline := time.Now().Add(wait)

	for {
		err = Exclusive(f)
		if err == nil {
			return func() {
				_ = Unlock(f)
				_ = f.Close()
			}, nil
		}

		if !errors.Is(err, ErrHeld) || time.Now().After(deadline) {
			_ = f.Close()

			return nil, fmt.Errorf("lock %s: %w", path, err)
		}

		time.Sleep(retryDelay)
	}
}
