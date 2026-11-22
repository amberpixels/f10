package state

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Every verb is recorded as state_<verb>: a kind the ledger has no payload
// for would drop the call's row.
func TestEveryVerbsKindIsRegistered(t *testing.T) {
	verbs := 0

	for _, c := range Command().Commands {
		if !c.SkipFlagParsing { // show, clear and prune record nothing
			continue
		}

		verbs++

		if _, err := ledger.NewEvent("state_"+c.Name, &ledger.ClaimPayload{}); err != nil {
			t.Errorf("verb %s: %v", c.Name, err)
		}
	}

	if verbs < 9 {
		t.Errorf("found %d verbs", verbs)
	}
}

// A catchup's turn may not write ship's phase or stop kind, which the drive
// reads to decide what to send next; the state it found stays as it was.
func TestACatchupTurnWritesNoPhase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "s1")

	start := state.Fresh("s1")
	start.Root, start.Skill, start.Plan, start.Ship, start.Leaf, start.At = "/work/acme", "ship", "done", "partial", "pr", 4
	start.Pipeline, start.Stop, start.Turn = []string{"implement", "pr"}, state.StopGuard, state.TurnCatchup

	if err := start.Save(t.Context(), dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"set", "ship", "running", "pr"}, {"stop", "guard"}} {
		err := Command().Run(t.Context(), append([]string{"state"}, args...))
		if !errors.Is(err, state.ErrPhaselessTurn) {
			t.Errorf("%v: err = %v, want the phaseless-turn refusal", args, err)
		}
	}

	r, err := state.Load(state.Path(dir, "s1"))
	if err != nil {
		t.Fatal(err)
	}

	if r.Ship != "partial" || r.Stop != state.StopGuard || r.Leaf != "pr" {
		t.Errorf("ship %s stop %q leaf %q, want the guard's partial stop at pr untouched", r.Ship, r.Stop, r.Leaf)
	}
}

// An ask prints as the banner the model shows verbatim, and the inbox
// records the pane that asked.
func TestAskPrintsTheBannerAndKeepsThePane(t *testing.T) {
	home, inbox := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("F10_STATE_DIR", t.TempDir())
	t.Setenv("F10_INBOX_DIR", inbox)
	t.Setenv("F10_SESSION_ID", "s1")
	t.Setenv("HERDR_PANE_ID", "w1:p2")
	t.Setenv("HERDR_ENV", "")

	if err := os.MkdirAll(filepath.Join(home, ".f10"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(home, ".f10", "inbox.md"),
		[]byte("---\nnotify: off\n---\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	t.Chdir(testkit.ScopeRepo(t, t.TempDir(), "app"))

	var out bytes.Buffer

	for _, args := range [][]string{{"task", "FT-12"}, {"set", "ship", "running", "resolve"}, {"ask", "1. Deploy? [yes | no]"}} {
		cmd := Command()
		cmd.Writer = &out

		if err := cmd.Run(t.Context(), append([]string{"state"}, args...)); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}

	open, err := asks.Open(inbox)
	if err != nil || len(open) != 1 || open[0].Pane != "w1:p2" {
		t.Fatalf("inbox = %+v, %v; want one ask from pane w1:p2", open, err)
	}

	if want := asks.Banner(open[0], ""); out.String() != want {
		t.Errorf("printed:\n%s\nwant the banner:\n%s", out.String(), want)
	}
}

// An ask that lists its choices in prose still raises, exits 0 and prints
// its banner, and warns on stderr; its context reaches the inbox.
func TestAskWarnsOnChoicesInProse(t *testing.T) {
	home, inbox := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("F10_STATE_DIR", t.TempDir())
	t.Setenv("F10_INBOX_DIR", inbox)
	t.Setenv("F10_SESSION_ID", "s1")
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_ENV", "")

	if err := os.MkdirAll(filepath.Join(home, ".f10"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(home, ".f10", "inbox.md"),
		[]byte("---\nnotify: off\n---\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	t.Chdir(testkit.ScopeRepo(t, t.TempDir(), "app"))

	var out, errOut bytes.Buffer

	for _, args := range [][]string{
		{"task", "FT-12"},
		{"ask", "1. What next? Options: 1) leave it red, 2) fix it, 3) rerun", "Lint failed on run 42."},
	} {
		cmd := Command()
		cmd.Writer, cmd.ErrWriter = &out, &errOut

		if err := cmd.Run(t.Context(), append([]string{"state"}, args...)); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}

	if !bytes.Contains(errOut.Bytes(), []byte("f10-state: question 1 lists choices in its text")) {
		t.Errorf("stderr = %q, want the choices warning", errOut.String())
	}

	open, err := asks.Open(inbox)
	if err != nil || len(open) != 1 || open[0].Context != "Lint failed on run 42." {
		t.Fatalf("inbox = %+v, %v; want one ask with its context", open, err)
	}

	if !bytes.Contains(out.Bytes(), []byte(asks.AskIcon+" FT-12")) {
		t.Errorf("stdout = %q, want the banner", out.String())
	}
}
