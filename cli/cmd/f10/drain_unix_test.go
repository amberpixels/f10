//go:build unix

package main

import (
	"testing"
	"time"
)

// Unwatching ends the goroutine that ranges over the drain channel, so a
// drive leaves nothing behind once it returns.
func TestUnwatchEndsTheDrainLoop(t *testing.T) {
	drain, unwatch := watchDrain(true)

	done := make(chan struct{})

	go func() {
		defer close(done)

		for range drain {
		}
	}()

	unwatch()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the drain loop still runs after unwatch")
	}
}
