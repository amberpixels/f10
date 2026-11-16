package state

import (
	"errors"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
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
