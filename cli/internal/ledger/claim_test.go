package ledger

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/state"
)

func fullRun() *state.Run {
	return &state.Run{
		Capture: "done", Plan: "prior", Ship: "running", Leaf: "pr", Final: "pr",
		Note: "n", Next: "x", Ask: "a", Wait: "FT-2", Done: "catchup",
		Pipeline: []string{"implement", "pr"}, Labels: []string{"", "pr-polish"}, At: 2,
	}
}

// Stored claims are read by these keys: renaming one orphans every row.
func TestEncodeClaimKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  *state.Run
		want []string
	}{
		{"full", fullRun(), []string{
			"args", "ask", "at", "capture", "done", "final", "labels", "leaf", "next", "note", "pipeline", "plan", "ship", "wait",
		}},
		{"minimal", &state.Run{}, []string{"args", "capture", "plan", "ship"}},
	} {
		got := slices.Sorted(maps.Keys(EncodeClaim(tc.run, nil)))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: keys %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClaimRoundTripsThroughTheDatabase(t *testing.T) {
	path := tempPath(t)
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	args := []string{"ship", "running", "pr"}

	openT(t, path).Append(context.Background(), Event{
		At: at, Session: "s1", Task: "FT-1", Checkout: "/co", Kind: StateSet, Source: Claimed,
		Payload: NewPayload(EncodeClaim(fullRun(), args)),
	})

	c := DecodeClaim(list(t, path, Filter{})[0])

	want := *fullRun()
	want.Session, want.Task, want.Root = "s1", "FT-1", "/co"

	if !c.Run.Updated.Equal(at) {
		t.Errorf("updated %v", c.Run.Updated)
	}

	c.Run.Updated = time.Time{}

	if !slices.Equal(c.Args, args) || !reflect.DeepEqual(c.Run, want) {
		t.Errorf("decoded %+v %+v", c.Args, c.Run)
	}
}

// A row in the shape claims have always been stored in decodes unchanged.
func TestDecodeClaimReadsAStoredRow(t *testing.T) {
	var p Payload
	if err := json.Unmarshal([]byte(`{"args":["ship","running","pr[pr-polish]"],"capture":"","plan":"prior",`+
		`"ship":"running","leaf":"pr","pipeline":["implement","pr"],"labels":["","pr-polish"],"at":2}`), &p); err != nil {
		t.Fatal(err)
	}

	c := DecodeClaim(Event{Task: "FT-1", Payload: NewPayload(p)})
	if c.Args[2] != "pr[pr-polish]" || c.Run.Plan != "prior" || c.Run.At != 2 ||
		c.Run.LabelAt(c.Run.At) != "pr-polish" || !slices.Equal(c.Run.Pipeline, []string{"implement", "pr"}) {
		t.Errorf("decoded %+v %+v", c.Args, c.Run)
	}
}

func TestDecodeClaimReadsPendingAsUpcoming(t *testing.T) {
	var p Payload
	if err := json.Unmarshal([]byte(`{"capture":"prior","plan":"done","ship":"pending"}`), &p); err != nil {
		t.Fatal(err)
	}

	if c := DecodeClaim(Event{Task: "FT-1", Payload: NewPayload(p)}); c.Run.Ship != state.Upcoming {
		t.Errorf("ship = %q, want %q", c.Run.Ship, state.Upcoming)
	}
}
