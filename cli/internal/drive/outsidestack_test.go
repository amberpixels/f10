package drive

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A task stacked on a started base outside the list ends at its PR, since
// finish would refuse its stacked PR and nothing parks it until the base lands.
func TestOutsideStackDropsFinish(t *testing.T) {
	t.Parallel()

	stacked := start.OpenedTask{Dep: &launch.Dependency{Task: "GH-9", Branch: "GH-9/base"}}
	listed := &DriveRun{Task: DriveTask{Ref: testkit.Gh1}}
	listed.Base = &DriveRun{}

	for _, tc := range []struct {
		name     string
		r        *DriveRun
		o        start.OpenedTask
		planOnly bool
		finishes bool
	}{
		{"outside base", &DriveRun{Task: DriveTask{Ref: testkit.Gh1}}, stacked, false, false},
		{"listed base parks finish", listed, stacked, false, true},
		{"no base", &DriveRun{Task: DriveTask{Ref: testkit.Gh1}}, start.OpenedTask{}, false, true},
		{"--plan runs no finish anyway", &DriveRun{Task: DriveTask{Ref: testkit.Gh1}}, stacked, true, true},
	} {
		fl, note := outsideStack(tc.r, tc.o, runplan.ShipFlow{Finishes: true}, tc.planOnly)
		if fl.Finishes != tc.finishes || (note != "") == tc.finishes {
			t.Errorf("%s: finishes %v, note %q", tc.name, fl.Finishes, note)
		}

		if note != "" && !strings.Contains(note, "stacked on GH-9") {
			t.Errorf("%s: note %q", tc.name, note)
		}
	}
}
