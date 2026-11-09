package state

import (
	"bytes"
	"testing"
	"time"
)

func TestRunShow(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

	var empty bytes.Buffer

	(&Run{Updated: now.Add(-5 * time.Hour)}).Show(&empty, now)

	if got := empty.String(); got != wantShowEmpty {
		t.Errorf("empty run:\n%s\nwant:\n%s", got, wantShowEmpty)
	}
}

const wantShowEmpty = `task     -
url      -
capture  ○ 
plan     ○ 
ship     ○ 
updated  5h ago
`
