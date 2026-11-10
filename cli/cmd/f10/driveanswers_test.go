package main

import (
	"maps"
	"testing"
	"time"
)

// A keyed answer goes to its task before the bare one; an answer the agent
// heard is followed, never sent, and never kept for a resume.
func TestAnswerBook(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)

	b := newAnswerBook()
	b.give(answer{text: "1. yes"})
	b.give(answer{task: "GH-1", text: "1. local"})
	b.give(answer{task: "GH-2", text: "2. b", from: heardInbox, at: at})

	if want := map[string]string{"": "1. yes", "GH-1": "1. local"}; !maps.Equal(b.pending(), want) {
		t.Errorf("pending = %v, want %v", b.pending(), want)
	}

	if got := b.take("GH-2"); got != "1. yes" {
		t.Errorf("take(GH-2) = %q, want the bare answer, since its own was heard", got)
	}

	if when, ok := b.heard("GH-2"); !ok || !when.Equal(at) {
		t.Errorf("heard(GH-2) = %v %v", when, ok)
	}

	if _, ok := b.heard("GH-2"); ok {
		t.Error("a heard answer was followed twice")
	}

	if got := b.take("GH-1"); got != "1. local" {
		t.Errorf("take(GH-1) = %q", got)
	}

	if got := b.take("GH-1"); got != "" {
		t.Errorf("take(GH-1) again = %q, want nothing left", got)
	}

	// an inbox answer replaces the flag's for the same task
	b.give(answer{task: "GH-3", text: "from the flag"})
	b.give(answer{task: "GH-3", text: "from the inbox", from: fromInbox})

	if a, _ := b.keyed("GH-3"); a.text != "from the inbox" {
		t.Errorf("keyed(GH-3) = %q", a.text)
	}

	b.clear()

	if len(b.pending()) != 0 {
		t.Errorf("pending after clear = %v", b.pending())
	}
}
