//go:build unix

package daemon

import (
	"testing"
	"time"
)

// A drain waits for a start or a finish in flight to reply, and a stop
// --now cuts that wait short.
func TestDrainAwaitsTheCallsInFlight(t *testing.T) {
	t.Parallel()

	s := &Server{escalate: make(chan struct{}), unary: 1}

	go func() {
		time.Sleep(200 * time.Millisecond)
		s.mu.Lock()
		s.unary = 0
		s.mu.Unlock()
	}()

	began := time.Now()
	s.awaitCalls(time.Minute)

	if waited := time.Since(began); waited < 150*time.Millisecond || waited > 10*time.Second {
		t.Errorf("waited %s, want the call's 200ms", waited)
	}

	s = &Server{escalate: make(chan struct{}), unary: 1}
	close(s.escalate)

	began = time.Now()
	s.awaitCalls(time.Minute)

	if waited := time.Since(began); waited > 10*time.Second {
		t.Errorf("a stop --now waited %s on a call in flight", waited)
	}
}
