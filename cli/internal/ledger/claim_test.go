package ledger

import (
	"context"
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
			"args", "ask", "at", "capture", "done", "final", "labels", "leaf", "next", "note", "pipeline", "plan", "ship", "v", "wait",
		}},
		{"minimal", &state.Run{}, []string{"args", "capture", "plan", "ship", "v"}},
	} {
		got := slices.Sorted(maps.Keys(storedKeys(t, MustEvent(StateSet, EncodeClaim(tc.run, nil)))))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: keys %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestClaimRoundTripsThroughTheDatabase(t *testing.T) {
	path := tempPath(t)
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	args := []string{"ship", "running", "pr"}

	e := MustEvent(StateSet, EncodeClaim(fullRun(), args))
	e.At, e.Session, e.Task, e.Checkout, e.Source = at, "s1", "FT-1", "/co", Claimed
	openT(t, path).Append(context.Background(), e)

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
	c := DecodeClaim(
		Event{
			Task: "FT-1",
			Kind: StateSet,
			Payload: rawPayload(t, `{"args":["ship","running","pr[pr-polish]"],"capture":"","plan":"prior",`+
				`"ship":"running","leaf":"pr","pipeline":["implement","pr"],"labels":["","pr-polish"],"at":2}`),
		},
	)
	if c.Args[2] != "pr[pr-polish]" || c.Run.Plan != "prior" || c.Run.At != 2 ||
		c.Run.LabelAt(c.Run.At) != "pr-polish" || !slices.Equal(c.Run.Pipeline, []string{"implement", "pr"}) {
		t.Errorf("decoded %+v %+v", c.Args, c.Run)
	}
}

func TestDecodeClaimReadsPendingAsUpcoming(t *testing.T) {
	p := rawPayload(t, `{"capture":"prior","plan":"done","ship":"pending"}`)
	if c := DecodeClaim(Event{Task: "FT-1", Kind: StateSet, Payload: p}); c.Run.Ship != state.Upcoming {
		t.Errorf("ship = %q, want %q", c.Run.Ship, state.Upcoming)
	}
}
