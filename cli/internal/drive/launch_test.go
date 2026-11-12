package drive

import (
	"bytes"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A drive's fresh task gets its first skill on the agent's command line,
// never typed into the pane.
func TestDriveLaunchesWithTheFirstSkill(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.task("GH-1", hostops.TaskInfo{Body: "first"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	want := "herdr agent start " + testkit.AgentOf("GH-1") + " --kind claude --pane pane:1 -- " +
		skillPrompt("judge", "GH-1", nil, runplan.ShipFlow{}, false)
	if len(fx.f.Launches) != 1 || fx.f.Launches[0] != want {
		t.Errorf("launches = %q, want %q", fx.f.Launches, want)
	}
}

// The drive checks a typed send once, EnterRetry after it, and never one
// that went on the agent's command line.
func TestDriveRetriesEnterOnceForATypedSend(t *testing.T) {
	m := testkit.UseAgents(t)
	m.Screens = []string{testkit.ScreenWith("❯ " + testkit.SentText)}

	seam.Swap(t, &launch.EnterRetry, 10*time.Second)

	now := time.Now()
	in := &DriveInput{
		Now: func() time.Time { return now },
	}

	typed := &DriveRun{
		Task:   DriveTask{Ref: testkit.Gh1},
		Skill:  "ship",
		SentAt: now.Add(-11 * time.Second),
		Typed:  testkit.SentText,
	}
	typed.O.Path = testkit.FwdWT

	launched := &DriveRun{Task: DriveTask{Ref: testkit.Gh1}, Skill: "ship", SentAt: now.Add(-11 * time.Second)}
	recent := &DriveRun{
		Task:   DriveTask{Ref: testkit.Gh1},
		Skill:  "ship",
		SentAt: now.Add(-time.Second),
		Typed:  testkit.SentText,
	}

	var out bytes.Buffer

	in.Out = &out

	for range 2 {
		in.RetryEnter(t.Context(), typed)
		in.RetryEnter(t.Context(), launched)
		in.RetryEnter(t.Context(), recent)
	}

	if len(m.Enters) != 1 || !bytes.Contains(out.Bytes(), []byte("Enter resent")) {
		t.Errorf("%d Enters, output:\n%s", len(m.Enters), out.String())
	}
}
