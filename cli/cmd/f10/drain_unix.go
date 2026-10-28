//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// watchDrain catches SIGUSR1, the daemon's request that a foreground drive
// stop at its next tick; nil, and nothing caught, outside the foreground.
func watchDrain(fore bool) (<-chan os.Signal, func()) {
	if !fore {
		return nil, func() {}
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)

	return ch, func() { signal.Stop(ch) }
}
