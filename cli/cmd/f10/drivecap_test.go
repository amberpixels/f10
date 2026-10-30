package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// capRuns is n unopened tasks with no branch, GH-1 first.
func capRuns(n int) []*driveRun {
	runs := make([]*driveRun, n)
	for i := range runs {
		runs[i] = &driveRun{task: driveTask{ref: ref.Ref{ID: fmt.Sprintf("GH-%d", i+1)}}}
	}

	return runs
}

// openRun marks r open in its own workspace, its chain under way.
func openRun(r *driveRun) {
	r.opened = true
	r.o.path = "/code/repo." + r.task.ref.ID
	r.o.ws.ID = "ws:" + r.task.ref.ID
}

// liveGate is a cap gate over the workspaces of every opened run.
func liveGate(in *driveInput) *openGate {
	g := &openGate{on: true, limit: in.parallel, trees: map[string]string{}}

	for _, r := range in.runs {
		if r.opened {
			g.wss = append(g.wss, agentctl.Workspace{ID: r.o.ws.ID, Path: r.o.path, Status: agentctl.StatusIdle})
		}
	}

	for _, r := range in.runs {
		if (r.end == nil || r.end.code == exitAsk) && g.workspace(r) != nil {
			g.open++
		}
	}

	return g
}

// pass is one startRunnable pass without the shell: the gate decides, and
// an admitted task opens with its chain under way.
func pass(in *driveInput, g *openGate) {
	for _, r := range g.order(in.runs) {
		if r.opened || r.end != nil {
			continue
		}

		if !g.admit(in, r) {
			in.queue(io.Discard, r)

			continue
		}

		in.unqueue(r)
		openRun(r)
	}
}

// FT-349's first acceptance run: with --parallel 4, ten independent tasks
// never have more than four on a skill at once, and all ten finish.
func TestCapHoldsTenTasksToFour(t *testing.T) {
	in := &driveInput{parallel: 4, out: io.Discard}
	in.runs = capRuns(10)

	for tick := range 20 {
		pass(in, liveGate(in))

		if n := in.busy(nil); n > 4 {
			t.Fatalf("tick %d: %d tasks on a skill, over the cap of 4", tick, n)
		}

		// the oldest task on a skill finishes, its workspace closed by finish
		for _, r := range in.runs {
			if r.holdsSlot() {
				r.end = &driveResult{row: "finished"}
				r.o.path = ""

				break
			}
		}
	}

	for _, r := range in.runs {
		if r.end == nil || r.queued {
			t.Errorf("%s did not finish: queued %v", r.task.ref.ID, r.queued)
		}
	}
}

// The second acceptance run: a task parked on an ask frees its slot, and
// the next queued task opens on the same pass.
func TestCapAskFreesTheSlot(t *testing.T) {
	in := &driveInput{parallel: 2, out: io.Discard}
	in.runs = capRuns(4)

	pass(in, liveGate(in))

	if !in.runs[2].queued || !in.runs[3].queued || in.runs[2].opened {
		t.Fatalf("GH-3 and GH-4 should queue behind the cap of 2")
	}

	in.runs[0].end = &driveResult{row: "asked at judge", code: exitAsk}
	pass(in, liveGate(in))

	if !in.runs[2].opened || in.runs[2].queued {
		t.Error("GH-3 did not take the slot GH-1's ask freed")
	}

	if in.runs[3].opened || !in.runs[3].queued {
		t.Error("GH-4 opened past the cap")
	}
}

// An open task that gave its slot up, parked or queued, sends its next
// skill only once a slot is free, unless its agent is already mid-turn.
func TestCapSlotFree(t *testing.T) {
	in := &driveInput{parallel: 1, out: io.Discard}
	in.runs = capRuns(2)
	openRun(in.runs[0])
	openRun(in.runs[1])
	in.runs[1].parked = true

	if in.slotFree(in.runs[1]) {
		t.Error("a parked task took a slot GH-1 holds")
	}

	in.wss = []agentctl.Workspace{{ID: "ws:GH-2", Status: agentctl.StatusWorking}}
	if !in.slotFree(in.runs[1]) {
		t.Error("a task mid-turn was held back: the drive cannot stop its turn")
	}

	in.wss = nil
	in.runs[0].waiting = true

	if !in.slotFree(in.runs[1]) {
		t.Error("finish waiting on its base still held the slot")
	}

	in.parallel = 0
	in.runs[0].waiting = false

	if !in.slotFree(in.runs[1]) {
		t.Error("a drive with no cap held a task back")
	}
}

// Asks keep their agents open, so once they fill twice the cap no new task
// opens, though no task is on a skill.
func TestCapOpenCeiling(t *testing.T) {
	in := &driveInput{parallel: 1, out: io.Discard}
	in.runs = capRuns(3)

	for _, r := range in.runs[:2] {
		openRun(r)
		r.end = &driveResult{row: "asked at plan", code: exitAsk}
	}

	pass(in, liveGate(in))

	if in.runs[2].opened || !in.runs[2].queued {
		t.Error("GH-3 opened with two asks holding agents open under a cap of 1")
	}

	// a halted task's agent is the user's, so it frees the ceiling
	in.runs[1].end = &driveResult{row: "halted at plan", code: exitHalted}
	pass(in, liveGate(in))

	if !in.runs[2].opened {
		t.Error("GH-3 stayed queued behind a halted task's agent")
	}
}

// After a resume, a task whose agent an earlier run left open goes ahead of
// a fresh one: it adds no session, and a turn still running opens past the cap.
func TestCapResumeTakesOpenAgentsFirst(t *testing.T) {
	in := &driveInput{parallel: 1, out: io.Discard}
	in.runs = capRuns(3)
	in.runs[1].task.branch = "GH-2"
	in.runs[2].task.branch = "GH-3"

	g := &openGate{
		on: true, limit: 1, open: 2,
		trees: map[string]string{"GH-2": "/code/repo.GH-2", "GH-3": "/code/repo.GH-3"},
		wss: []agentctl.Workspace{
			{ID: "ws:GH-2", Path: "/code/repo.GH-2", Status: agentctl.StatusIdle},
			{ID: "ws:GH-3", Path: "/code/repo.GH-3", Status: agentctl.StatusWorking},
		},
	}

	pass(in, g)

	if !in.runs[1].opened || in.runs[0].opened {
		t.Error("the fresh GH-1 took the slot ahead of GH-2's open agent")
	}

	if !in.runs[2].opened {
		t.Error("GH-3's running turn was queued: the drive cannot stop it")
	}
}

// Through the drive: with --parallel 1, GH-2 queues until GH-1's judge is
// done, then opens, and the report has both done.
func TestDriveQueuesPastTheCap(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge", "--parallel", "1")
	fx.task("GH-1", taskInfo{body: "first"}, "")
	fx.task("GH-2", taskInfo{body: "second"}, "")
	fx.f.script(worktrees,
		porcelain(), porcelain(), porcelain("GH-1", "/code/repo.GH-1"),
		porcelain("GH-1", "/code/repo.GH-1"), porcelain("GH-1", "/code/repo.GH-1"),
		porcelain("GH-1", "/code/repo.GH-1"),
		porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.opens("GH-2", "ws:GH-2", "pane:2")
	fx.expects("GH-1", "judge")
	fx.expects("GH-2", "judge")
	fx.f.script(wsList,
		unopened, unopened, listing("GH-1", "idle"), // the gate's look, GH-1's open, the look before its judge
		listing("GH-1", "working"), listing("GH-1", "idle"), // GH-1's turn; GH-2 still queued
		listing("GH-1", "idle"), listing("GH-1", "idle", "GH-2", "idle"), // GH-2's open, the look before its judge
		listing("GH-1", "idle", "GH-2", "working"), listing("GH-1", "idle", "GH-2", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		"--create GH-1", agentOf("GH-1") + " /f10:judge", "--create GH-2", agentOf("GH-2") + " /f10:judge",
	}, "herdr agent prompt", "wt switch")

	out := fx.out.String()
	for _, want := range []string{"GH-2: queued, 1 running", "GH-1:    done: judge", "GH-2:    done: judge"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
