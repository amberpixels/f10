package drive

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// An end a task's path returns, wrapped on its way up, still ends that task
// at the tick; any other error goes on up and ends nothing.
func TestOnTaskAppliesAReturnedEnd(t *testing.T) {
	var out bytes.Buffer

	in := &DriveInput{
		Out: &out,
	}
	r := &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: "GH-1"}}, Phase: AtRunning}

	wrapped := fmt.Errorf("sending: %w", Halt("GH-1", "ship", "no agent"))
	if err := in.OnTask(t.Context(), r, wrapped); err != nil {
		t.Fatalf("onTask = %v, want the end applied", err)
	}

	if r.End == nil || r.End.Code != ui.ExitHalted || !strings.Contains(out.String(), "GH-1 halted at ship: no agent") {
		t.Fatalf("end = %+v\n%s", r.End, out.String())
	}

	other := &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: "GH-2"}}, Phase: AtRunning}
	boom := errors.New("herdr is gone")

	if err := in.OnTask(t.Context(), other, boom); !errors.Is(err, boom) || other.End != nil {
		t.Fatalf("onTask = %v, end = %+v: want the error passed up", err, other.End)
	}
}

// A send reads the tick's look, except for a task opened since that look,
// which the look cannot show: that one lists the workspaces again.
func TestLookReListsOnlyATaskOpenedThisTick(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(testkit.WsList, listing("GH-1", "idle"))

	in := &DriveInput{StateDir: t.TempDir(), TTL: state.DefaultTTL, Now: time.Now}
	in.Tick = &TickView{
		Wss: []agentctl.Workspace{{ID: "ws:GH-1", Path: "/code/repo.GH-1", Status: agentctl.StatusIdle}},
	}
	r := &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: "GH-1"}}, O: start.OpenedTask{Path: "/code/repo.GH-1"}}
	r.O.Ws.ID = "ws:GH-1"

	if _, _, err := in.Look(t.Context(), r); err != nil || f.Called(testkit.WsList) {
		t.Fatalf("look = %v, listed %v: want the tick's look", err, f.Called(testkit.WsList))
	}

	in.View().JustOpened(r)

	if _, _, err := in.Look(t.Context(), r); err != nil || !f.Called(testkit.WsList) {
		t.Fatalf("look = %v, listed %v: want a fresh list for a task opened this tick", err, f.Called(testkit.WsList))
	}
}
