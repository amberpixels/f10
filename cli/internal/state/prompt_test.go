package state

import (
	"testing"
	"time"
)

func TestLastPromptIsTheNewestInTheRoot(t *testing.T) {
	dir := t.TempDir()
	at := time.UnixMilli(1_800_000_000_123)

	for _, m := range []struct {
		session, root string
		at            time.Time
	}{
		{"a", "/code/repo.GH-1", at},
		{"b", "/code/repo.GH-1", at.Add(time.Second)},
		{"c", "/code/repo.GH-2", at.Add(time.Hour)},
	} {
		if err := MarkPrompt(dir, m.session, m.root, m.at); err != nil {
			t.Fatal(err)
		}
	}

	p, ok := LastPrompt(dir, "/code/repo.GH-1")
	if !ok || p.Session != "b" || !p.At.Equal(at.Add(time.Second)) {
		t.Errorf("LastPrompt = %+v, %v", p, ok)
	}

	if _, ok := LastPrompt(dir, "/code/repo.GH-3"); ok {
		t.Error("a root with no mark has one")
	}
}
