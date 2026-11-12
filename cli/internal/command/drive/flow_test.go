package drive_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	startcmd "github.com/amberpixels/f10/cli/internal/command/start"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/flow"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A rerun of drive keeps the flow a branch records; a branch with none,
// or one recording a flow no longer declared, takes the run's.
func TestDriveKeepsTheRecordedFlow(t *testing.T) {
	declared := map[string]runplan.ShipFlow{"": {}, "local": {Name: "local"}}
	resolve := func(name string) (runplan.ShipFlow, error) {
		if fl, ok := declared[name]; ok {
			return fl, nil
		}

		return runplan.ShipFlow{}, os.ErrNotExist
	}

	for _, tc := range []struct {
		recorded, want, note string
		writes               bool
	}{
		{recorded: "", want: "default", writes: true},
		{recorded: "default", want: "default"},
		{recorded: "local", want: "local", note: "kept the local flow"},
		{recorded: "gone", want: "default", note: "no longer declares", writes: true},
	} {
		f := testkit.NewFakes(t)
		f.Meta.Seed("GH-1", branchmeta.Flow, tc.recorded)

		in := &drive.DriveInput{
			Main:    "/code/repo",
			Resolve: resolve,
		}

		fl, note, err := in.KeepFlow(t.Context(), "GH-1")
		if err != nil || fl.Recorded() != tc.want || !strings.Contains(note, tc.note) ||
			f.Called("set branch.GH-1."+string(branchmeta.Flow)+" default") != tc.writes {
			t.Errorf("recorded %q: flow %q, note %q, err %v, calls %v", tc.recorded, fl.Recorded(), note, err, f.Calls)
		}
	}
}

// A task runs the skills of the flow it keeps from its branch: a planless
// run still plans a task whose recorded flow leads with plan.
func TestSkillsOfAKeptFlow(t *testing.T) {
	direct := runplan.ShipFlow{Name: "direct", Planless: true}
	in := &drive.DriveInput{
		Flow: direct,
	}

	if got := strings.Join(in.Skills(&drive.DriveRun{Flow: direct}), " "); got != "ship finish" {
		t.Errorf("the run's own flow = %q", got)
	}

	if got := strings.Join(in.Skills(&drive.DriveRun{Flow: runplan.ShipFlow{}}), " "); got != "plan ship finish" {
		t.Errorf("a kept default flow = %q, want plan first and finish kept", got)
	}

	spike := &drive.DriveRun{Flow: runplan.ShipFlow{Finish: "off"}, Finishes: true}
	if got := strings.Join(in.Skills(spike), " "); got != "plan ship finish" {
		t.Errorf("a spike closing on a pace without finish = %q, want finish added", got)
	}
}

// With flow_by_area declared and no flow named, ship picks the flow from
// the task's area: start and drive send ship alone, and record nothing.
func TestFlowByArea(t *testing.T) {
	eff := &facts.Effective{
		Flows: map[string]flow.Flow{
			"default": flow.Of([]string{"plan", "implement", "pr"}), "direct": flow.Of([]string{"implement", "push"}),
		},
		FlowByArea: map[string]string{"ui": "direct"},
	}

	fl, err := testkit.PacedFlowIn(eff, nil, "", "")
	if err != nil || !fl.ByArea || fl.Plans() || fl.Recorded() != "" {
		t.Fatalf("no flow named = %+v, %v", fl, err)
	}

	if named, _ := testkit.PacedFlowIn(eff, nil, "", "default"); named.ByArea || !named.Plans() {
		t.Errorf("a named flow wins over the area map: %+v", named)
	}

	if got := launch.Prompt(startcmd.ModeDefault, fl, "GH-1", nil, "", nil, ""); got != "/f10:ship GH-1 --driven" {
		t.Errorf("prompt = %q, want ship alone", got)
	}

	if got := strings.Join(drive.DriveSkills(fl), " "); got != "ship finish" {
		t.Errorf("drive skills = %q, want ship plans as the area's flow says", got)
	}

	f := testkit.NewFakes(t)

	in := &drive.DriveInput{
		Main: "/code/repo",
		Flow: fl,
	}
	if kept, note, err := in.KeepFlow(t.Context(), "GH-1"); err != nil || kept != fl || note != "" ||
		slices.ContainsFunc(f.Calls, func(c string) bool { return strings.HasPrefix(c, "set branch.") }) {
		t.Errorf("keepFlow = %+v, %q, %v; calls %v", kept, note, err, f.Calls)
	}
}
