package drive

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A pace that skips plan drops /f10:plan from drive's skills, and its
// finish word decides whether they end in finish.
func TestPaceShapesTheDriveSkills(t *testing.T) {
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": flow.Of(strings.Fields("plan judge implement pr"))}}
	set := facts.NewPaceSet()

	for pace, want := range map[string]string{
		"":       "plan ship finish",
		"medium": "plan ship finish",
		"fast":   "plan ship finish",
		"slow":   "plan ship",
		"ultra":  "ship finish",
	} {
		fl, err := testkit.PacedFlowIn(eff, set, pace, "")
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(DriveSkills(fl), " "); got != want {
			t.Errorf("%q: skills %q, want %q", pace, got, want)
		}
	}
}

// A run's --pace wins and is recorded; a rerun naming none keeps the
// pace its branch records.
func TestDriveKeepsTheRecordedPace(t *testing.T) {
	paced := func(fl runplan.ShipFlow, pace string) (runplan.ShipFlow, error) {
		fl.Pace = pace

		return fl, nil
	}

	for _, tc := range []struct {
		recorded, flag, run, want, note string
		writes                          bool
	}{
		{recorded: "", flag: "fast", run: "fast", want: "fast", writes: true},
		{recorded: "", run: "slow", want: "slow"},
		{recorded: "fast", flag: "fast", run: "fast", want: "fast"},
		{recorded: "ultra", flag: "slow", run: "slow", want: "slow", writes: true},
		{recorded: "ultra", run: "fast", want: "ultra", note: "kept the ultra pace"},
	} {
		f := testkit.NewFakes(t)
		f.Meta.Seed("GH-1", branchmeta.Pace, tc.recorded)

		in := &DriveInput{
			Main:  "/code/repo",
			Args:  drivectl.DriveArgs{Pace: tc.flag},
			Paced: paced,
		}

		fl, note, err := in.KeepPace(t.Context(), "GH-1", runplan.ShipFlow{Pace: tc.run})
		if err != nil || fl.Pace != tc.want || !strings.Contains(note, tc.note) ||
			f.Called("set branch.GH-1."+string(branchmeta.Pace)+" "+tc.flag) != tc.writes {
			t.Errorf("recorded %q: pace %q, note %q, err %v, calls %v", tc.recorded, fl.Pace, note, err, f.Calls)
		}
	}
}

// A ceiling's finish off keeps finish out of drive's skills under a pace
// that finishes, unless that pace is excepted.
func TestCeilingShapesTheDriveChain(t *testing.T) {
	eff := &facts.Effective{Flows: map[string]flow.Flow{"default": flow.Of(strings.Fields("plan judge implement pr"))}}
	set := facts.NewPaceSet()
	set.Ceiling = facts.Pace{Finish: "off"}

	for _, pace := range []string{"medium", "fast"} {
		fl, err := testkit.PacedFlowIn(eff, set, pace, "")
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Join(DriveSkills(fl), " "); got != "plan ship" {
			t.Errorf("%s under finish off: %q", pace, got)
		}
	}

	set.Except = []string{"fast"}

	fl, err := testkit.PacedFlowIn(eff, set, "fast", "")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(DriveSkills(fl), " "); got != "plan ship finish" {
		t.Errorf("excepted fast: %q", got)
	}
}
