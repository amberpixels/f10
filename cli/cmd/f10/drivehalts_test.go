package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// An end a task's path returns, wrapped on its way up, still ends that task
// at the tick; any other error goes on up and ends nothing.
func TestOnTaskAppliesAReturnedEnd(t *testing.T) {
	var out bytes.Buffer

	in := &driveInput{
		out: &out,
	}
	r := &driveRun{task: driveTask{ref: ref.Ref{ID: "GH-1"}}, phase: atRunning}

	wrapped := fmt.Errorf("sending: %w", halt("GH-1", "ship", "no agent"))
	if err := in.onTask(t.Context(), r, wrapped); err != nil {
		t.Fatalf("onTask = %v, want the end applied", err)
	}

	if r.end == nil || r.end.code != ui.ExitHalted || !strings.Contains(out.String(), "GH-1 halted at ship: no agent") {
		t.Fatalf("end = %+v\n%s", r.end, out.String())
	}

	other := &driveRun{task: driveTask{ref: ref.Ref{ID: "GH-2"}}, phase: atRunning}
	boom := errors.New("herdr is gone")

	if err := in.onTask(t.Context(), other, boom); !errors.Is(err, boom) || other.end != nil {
		t.Fatalf("onTask = %v, end = %+v: want the error passed up", err, other.end)
	}
}

// A send reads the tick's look, except for a task opened since that look,
// which the look cannot show: that one lists the workspaces again.
func TestLookReListsOnlyATaskOpenedThisTick(t *testing.T) {
	f := newFakes(t)
	f.script(wsList, listing("GH-1", "idle"))

	in := &driveInput{stateDir: t.TempDir(), ttl: state.DefaultTTL, now: time.Now}
	in.tick = &tickView{
		wss: []agentctl.Workspace{{ID: "ws:GH-1", Path: "/code/repo.GH-1", Status: agentctl.StatusIdle}},
	}
	r := &driveRun{task: driveTask{ref: ref.Ref{ID: "GH-1"}}, o: openedTask{path: "/code/repo.GH-1"}}
	r.o.ws.ID = "ws:GH-1"

	if _, _, err := in.look(t.Context(), r); err != nil || f.called(wsList) {
		t.Fatalf("look = %v, listed %v: want the tick's look", err, f.called(wsList))
	}

	in.view().justOpened(r)

	if _, _, err := in.look(t.Context(), r); err != nil || !f.called(wsList) {
		t.Fatalf("look = %v, listed %v: want a fresh list for a task opened this tick", err, f.called(wsList))
	}
}
