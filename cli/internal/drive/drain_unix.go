//go:build unix

package drive

import (
	"os"
	"os/signal"
	"syscall"
)

// WatchDrain catches SIGUSR1, the daemon's request that a foreground drive
// stop at its next tick; nil, and nothing caught, outside the foreground.
func WatchDrain(fore bool) (<-chan os.Signal, func()) {
	if !fore {
		return nil, func() {}
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)

	// Stop guarantees no further send, so closing ends a loop ranging over ch
	return ch, func() {
		signal.Stop(ch)
		close(ch)
	}
}
