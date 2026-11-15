package state

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/ledger"
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
