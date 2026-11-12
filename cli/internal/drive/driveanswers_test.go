package drive

import (
	"maps"
	"testing"
	"time"
)

// A keyed answer goes to its task before the bare one; an answer the agent
// heard is followed, never sent, and never kept for a resume.
func TestAnswerBook(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)

	b := NewAnswerBook()
	b.Give(Answer{Text: "1. yes"})
	b.Give(Answer{Task: "GH-1", Text: "1. local"})
	b.Give(Answer{Task: "GH-2", Text: "2. b", From: HeardInbox, At: at})

	if want := map[string]string{"": "1. yes", "GH-1": "1. local"}; !maps.Equal(b.Pending(), want) {
		t.Errorf("pending = %v, want %v", b.Pending(), want)
	}

	if got := b.Take("GH-2"); got != "1. yes" {
		t.Errorf("take(GH-2) = %q, want the bare answer, since its own was heard", got)
	}

	if when, ok := b.Heard("GH-2"); !ok || !when.Equal(at) {
		t.Errorf("heard(GH-2) = %v %v", when, ok)
	}

	if _, ok := b.Heard("GH-2"); ok {
		t.Error("a heard answer was followed twice")
	}

	if got := b.Take("GH-1"); got != "1. local" {
		t.Errorf("take(GH-1) = %q", got)
	}

	if got := b.Take("GH-1"); got != "" {
		t.Errorf("take(GH-1) again = %q, want nothing left", got)
	}

	// an inbox answer replaces the flag's for the same task
	b.Give(Answer{Task: "GH-3", Text: "from the flag"})
	b.Give(Answer{Task: "GH-3", Text: "from the inbox", From: FromInbox})

	if a, _ := b.Keyed("GH-3"); a.Text != "from the inbox" {
		t.Errorf("keyed(GH-3) = %q", a.Text)
	}

	b.Clear()

	if len(b.Pending()) != 0 {
		t.Errorf("pending after clear = %v", b.Pending())
	}
}
