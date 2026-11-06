package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

func TestStateCallsAreRecordedAsClaimed(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))
	t.Setenv("F10_STATE_DIR", t.TempDir())
	t.Setenv("F10_SESSION_ID", "s1")

	for _, args := range [][]string{
		{"task", "GH-7", "https://github.com/acme/app/issues/7"},
		{"set", "ship", "blocked", "judge"},
		{"note", "push rejected", "-f was needed"},
		{"ask", "1. retry? [no | yes]"},
		{"final", "pr"},
	} {
		if out, code := runState(t, args...); code != 0 {
			t.Fatalf("%q: exit %d\n%s", args, code, out)
		}
	}

	// a typo is no claim
	runState(t, "set", "shp", "running")

	t.Setenv("F10_SESSION_ID", "s2")
	runState(t, "set", "plan", "running")

	var kinds []string

	for line := range strings.SplitSeq(strings.TrimSpace(runApp(t, "ledger", "dump", "--json", "--session",
		"s1").must(t)), "\n") {
		var e ledger.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%v: %s", err, line)
		}

		if e.Source != ledger.Claimed {
			t.Errorf("%s: source %s", e.Kind, e.Source)
		}

		kinds = append(kinds, e.Kind)

		if e.Kind == ledger.StateSet &&
			(e.Task != "GH-7" || e.Payload.Val["ship"] != "blocked" || e.Payload.Val["leaf"] != "judge") {
			t.Errorf("set event %+v", e)
		}
	}

	want := "state_task state_set state_note state_ask state_final"
	if got := strings.Join(kinds, " "); got != want {
		t.Errorf("kinds %q, want %q", got, want)
	}

	if out := runApp(t, "ledger", "dump", "--task", "GH-7").must(t); strings.Count(out, "\n") != 5 ||
		!strings.Contains(out, "claimed") {
		t.Errorf("dump by task:\n%s", out)
	}
}

func TestDumpWithNoDatabasePrintsNothing(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	if out := runApp(t, "ledger", "dump").must(t); out != "" {
		t.Errorf("dump printed %q", out)
	}
}
