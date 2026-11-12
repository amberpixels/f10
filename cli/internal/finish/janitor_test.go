package finish

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestJanitorDue(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), JanitorStamp)
	now := time.Now()

	if !JanitorDue(stamp, now) {
		t.Error("no stamp: not due")
	}

	if err := TouchStamp(stamp, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	if JanitorDue(stamp, now) {
		t.Error("an hour-old stamp: due")
	}

	if !JanitorDue(stamp, now.Add(JanitorEvery)) {
		t.Error("a stamp past the interval: not due")
	}
}

// After start or finish, a fresh stamp skips the janitor; a due one runs it, and its
// failure is a warning with the stamp moved, never an error.
func TestSweepIfDue(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "f10", JanitorStamp)
	now := time.Now()

	var runs int

	run := func(context.Context) ([]string, error) {
		runs++

		return []string{"removed GH-1/x"}, errors.New("gh did not answer")
	}

	var out bytes.Buffer

	SweepIfDue(t.Context(), &out, stamp, now, run)

	if runs != 1 || JanitorDue(stamp, now) {
		t.Fatalf("runs = %d, due after the run = %v", runs, JanitorDue(stamp, now))
	}

	want := []string{"janitor: removed GH-1/x", "janitor: warning: gh did not answer"}
	if got := strings.Split(strings.TrimSpace(out.String()), "\n"); !slices.Equal(got, want) {
		t.Errorf("out = %q", got)
	}

	SweepIfDue(t.Context(), &out, stamp, now.Add(time.Minute), run)

	if runs != 1 {
		t.Error("a fresh stamp ran the janitor again")
	}
}
