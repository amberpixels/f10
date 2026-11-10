package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// An end a task's path returns, wrapped on its way up, still ends that task
// at the tick; any other error goes on up and ends nothing.
func TestOnTaskAppliesAReturnedEnd(t *testing.T) {
	var out bytes.Buffer

	in := &driveInput{out: &out}
	r := &driveRun{task: driveTask{ref: ref.Ref{ID: "GH-1"}}, phase: atRunning}

	wrapped := fmt.Errorf("sending: %w", halt("GH-1", "ship", "no agent"))
	if err := in.onTask(t.Context(), &out, r, wrapped); err != nil {
		t.Fatalf("onTask = %v, want the end applied", err)
	}

	if r.end == nil || r.end.code != ui.ExitHalted || !strings.Contains(out.String(), "GH-1 halted at ship: no agent") {
		t.Fatalf("end = %+v\n%s", r.end, out.String())
	}

	other := &driveRun{task: driveTask{ref: ref.Ref{ID: "GH-2"}}, phase: atRunning}
	boom := errors.New("herdr is gone")

	if err := in.onTask(t.Context(), &out, other, boom); !errors.Is(err, boom) || other.end != nil {
		t.Fatalf("onTask = %v, end = %+v: want the error passed up", err, other.end)
	}
}
