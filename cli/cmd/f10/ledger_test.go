package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// Every test here runs commands that record to the ledger; none may reach
// the real ~/.f10.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "f10-ledger-")
	if err != nil {
		panic(err)
	}

	_ = os.Setenv(ledger.PathEnv, filepath.Join(dir, "f10.db"))
	// no test's ask reaches the user's inbox
	_ = os.Setenv("F10_ASKS_DIR", filepath.Join(dir, "asks"))
	askNotifier = nil

	code := m.Run()

	_ = os.RemoveAll(dir)

	os.Exit(code)
}

func runLedger(t *testing.T, args ...string) string {
	t.Helper()

	var out bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &out

	if err := app.Run(context.Background(), append([]string{"f10", "ledger"}, args...)); err != nil {
		t.Fatalf("%q: %v\n%s", args, err, out.String())
	}

	return out.String()
}

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

	for line := range strings.SplitSeq(strings.TrimSpace(runLedger(t, "dump", "--json", "--session", "s1")), "\n") {
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

	if out := runLedger(
		t,
		"dump",
		"--task",
		"GH-7",
	); strings.Count(out, "\n") != 5 ||
		!strings.Contains(out, "claimed") {
		t.Errorf("dump by task:\n%s", out)
	}
}

func TestDumpWithNoDatabasePrintsNothing(t *testing.T) {
	t.Setenv(ledger.PathEnv, filepath.Join(t.TempDir(), "f10.db"))

	if out := runLedger(t, "dump"); out != "" {
		t.Errorf("dump printed %q", out)
	}
}
