package drive

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// capRuns is n unopened tasks with no branch, GH-1 first.
func capRuns(n int) []*DriveRun {
	runs := make([]*DriveRun, n)
	for i := range runs {
		runs[i] = &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: fmt.Sprintf("GH-%d", i+1)}}}
	}

	return runs
}

// openRun marks r open in its own workspace, its chain under way.
func openRun(r *DriveRun) {
	r.Phase = AtRunning
	r.O.Path = "/code/repo." + r.Task.Ref.ID
	r.O.Ws.ID = "ws:" + r.Task.Ref.ID
}

// liveGate is a cap gate over the workspaces of every opened run.
func liveGate(in *DriveInput) *OpenGate {
	g := &OpenGate{On: true, Limit: in.Parallel, Trees: map[string]string{}}

	for _, r := range in.Runs {
		if r.Opened() {
			g.Wss = append(g.Wss, agentctl.Workspace{ID: r.O.Ws.ID, Path: r.O.Path, Status: agentctl.StatusIdle})
		}
	}

	for _, r := range in.Runs {
		if (r.End == nil || r.End.Code == ui.ExitAsk) && g.Workspace(r) != nil {
			g.Open++
		}
	}

	return g
}

// pass is the gate alone, without the shell: the gate decides, and
// an admitted task opens with its chain under way.
func pass(in *DriveInput, g *OpenGate) {
	for _, r := range g.Order(in.Runs) {
		if r.Opened() || r.End != nil {
			continue
		}

		if !g.Admit(in, r) {
			in.Queue(r)

			continue
		}

		in.Unqueue(r)
		openRun(r)
	}
}

// FT-349's first acceptance run: with --parallel 4, ten independent tasks
// never have more than four on a skill at once, and all ten finish.
func TestCapHoldsTenTasksToFour(t *testing.T) {
	in := &DriveInput{
		Out:      io.Discard,
		Parallel: 4,
	}
	in.Runs = capRuns(10)

	for tick := range 20 {
		pass(in, liveGate(in))

		if n := in.Busy(nil); n > 4 {
			t.Fatalf("tick %d: %d tasks on a skill, over the cap of 4", tick, n)
		}

		// the oldest task on a skill finishes, its workspace closed by finish
		for _, r := range in.Runs {
			if r.HoldsSlot() {
				r.End = &DriveResult{Row: "finished"}
				r.O.Path = ""

				break
			}
		}
	}

	for _, r := range in.Runs {
		if r.End == nil || r.Queued() {
			t.Errorf("%s did not finish: queued %v", r.Task.Ref.ID, r.Queued())
		}
	}
}

// The second acceptance run: a task parked on an ask frees its slot, and
// the next queued task opens on the same pass.
func TestCapAskFreesTheSlot(t *testing.T) {
	in := &DriveInput{
		Out:      io.Discard,
		Parallel: 2,
	}
	in.Runs = capRuns(4)

	pass(in, liveGate(in))

	if !in.Runs[2].Queued() || !in.Runs[3].Queued() || in.Runs[2].Opened() {
		t.Fatalf("GH-3 and GH-4 should queue behind the cap of 2")
	}

	in.Runs[0].End = &DriveResult{Row: "asked at judge", Code: ui.ExitAsk}
	pass(in, liveGate(in))

	if !in.Runs[2].Opened() || in.Runs[2].Queued() {
		t.Error("GH-3 did not take the slot GH-1's ask freed")
	}

	if in.Runs[3].Opened() || !in.Runs[3].Queued() {
		t.Error("GH-4 opened past the cap")
	}
}

// An open task that gave its slot up, parked or queued, sends its next
// skill only once a slot is free, unless its agent is already mid-turn.
func TestCapSlotFree(t *testing.T) {
	in := &DriveInput{
		Out:      io.Discard,
		Parallel: 1,
	}
	in.Runs = capRuns(2)
	openRun(in.Runs[0])
	openRun(in.Runs[1])
	in.Runs[1].Phase = AtParked

	if in.SlotFree(in.Runs[1]) {
		t.Error("a parked task took a slot GH-1 holds")
	}

	in.Wss = []agentctl.Workspace{{ID: "ws:GH-2", Status: agentctl.StatusWorking}}
	if !in.SlotFree(in.Runs[1]) {
		t.Error("a task mid-turn was held back: the drive cannot stop its turn")
	}

	in.Wss = nil
	in.Runs[0].Phase = AtWaiting

	if !in.SlotFree(in.Runs[1]) {
		t.Error("finish waiting on its base still held the slot")
	}

	in.Parallel = 0
	in.Runs[0].Phase = AtRunning

	if !in.SlotFree(in.Runs[1]) {
		t.Error("a drive with no cap held a task back")
	}
}

// Asks keep their agents open, so once they fill twice the cap no new task
// opens, though no task is on a skill.
func TestCapOpenCeiling(t *testing.T) {
	in := &DriveInput{
		Out:      io.Discard,
		Parallel: 1,
	}
	in.Runs = capRuns(3)

	for _, r := range in.Runs[:2] {
		openRun(r)
		r.End = &DriveResult{Row: "asked at plan", Code: ui.ExitAsk}
	}

	pass(in, liveGate(in))

	if in.Runs[2].Opened() || !in.Runs[2].Queued() {
		t.Error("GH-3 opened with two asks holding agents open under a cap of 1")
	}

	// a halted task's agent is the user's, so it frees the ceiling
	in.Runs[1].End = &DriveResult{Row: "halted at plan", Code: ui.ExitHalted}
	pass(in, liveGate(in))

	if !in.Runs[2].Opened() {
		t.Error("GH-3 stayed queued behind a halted task's agent")
	}
}

// After a resume, a task whose agent an earlier run left open goes ahead of
// a fresh one: it adds no session, and a turn still running opens past the cap.
func TestCapResumeTakesOpenAgentsFirst(t *testing.T) {
	in := &DriveInput{
		Out:      io.Discard,
		Parallel: 1,
	}
	in.Runs = capRuns(3)
	in.Runs[1].Task.Branch = "GH-2"
	in.Runs[2].Task.Branch = "GH-3"

	g := &OpenGate{
		On: true, Limit: 1, Open: 2,
		Trees: map[string]string{"GH-2": "/code/repo.GH-2", "GH-3": "/code/repo.GH-3"},
		Wss: []agentctl.Workspace{
			{ID: "ws:GH-2", Path: "/code/repo.GH-2", Status: agentctl.StatusIdle},
			{ID: "ws:GH-3", Path: "/code/repo.GH-3", Status: agentctl.StatusWorking},
		},
	}

	pass(in, g)

	if !in.Runs[1].Opened() || in.Runs[0].Opened() {
		t.Error("the fresh GH-1 took the slot ahead of GH-2's open agent")
	}

	if !in.Runs[2].Opened() {
		t.Error("GH-3's running turn was queued: the drive cannot stop it")
	}
}

// Through the drive: with --parallel 1, GH-2 queues until GH-1's judge is
// done, then opens, and the report has both done.
func TestDriveQueuesPastTheCap(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "judge", "--parallel", "1")
	fx.task("GH-1", hostops.TaskInfo{Body: "first"}, "")
	fx.task("GH-2", hostops.TaskInfo{Body: "second"}, "")
	fx.f.Script(testkit.Worktrees,
		testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"), testkit.Porcelain("GH-1", "/code/repo.GH-1"),
		testkit.Porcelain("GH-1", "/code/repo.GH-1", "GH-2", "/code/repo.GH-2"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.opens("GH-2", "ws:GH-2", "pane:2")
	fx.expects("GH-1", "judge")
	fx.expects("GH-2", "judge")
	fx.f.Script(testkit.WsList,
		unopened, unopened, listing("GH-1", "idle"), // the gate's look, GH-1's open, the look before its judge
		listing("GH-1", "working"), listing("GH-1", "idle"), // GH-1's turn; GH-2 still queued
		listing("GH-1", "idle"), listing("GH-1", "idle", "GH-2", "idle"), // GH-2's open, the look before its judge
		listing("GH-1", "idle", "GH-2", "working"), listing("GH-1", "idle", "GH-2", "idle"))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		"--create GH-1",
		testkit.AgentOf("GH-1") + " /f10:judge",
		"--create GH-2",
		testkit.AgentOf("GH-2") + " /f10:judge",
	}, "herdr agent prompt", "wt switch")

	out := fx.out.String()
	for _, want := range []string{"GH-2: queued, 1 running", "GH-1:    done: judge", "GH-2:    done: judge"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// Through the drive, the second acceptance run: under --parallel 1, GH-1's
// ask frees its slot on the tick it lands, GH-2 opens then while GH-3 stays
// queued, and GH-3 opens once GH-2 is done.
func TestDriveAskFreesTheSlot(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3", "judge", "--parallel", "1")

	for _, id := range []string{"GH-1", "GH-2", "GH-3"} {
		fx.task(id, hostops.TaskInfo{Body: id}, "")
	}

	one, two := []string{"GH-1", "/code/repo.GH-1"}, []string{"GH-2", "/code/repo.GH-2"}
	fx.f.Script(testkit.Worktrees,
		testkit.Porcelain(), testkit.Porcelain(one...), testkit.Porcelain(one...),
		testkit.Porcelain(append(one, two...)...), testkit.Porcelain(append(one, two...)...),
		testkit.Porcelain(append(append(one, two...), "GH-3", "/code/repo.GH-3")...))

	for i, id := range []string{"GH-1", "GH-2", "GH-3"} {
		fx.opens(id, "ws:"+id, fmt.Sprintf("pane:%d", i+1))
	}

	fx.f.Script(
		"herdr agent prompt "+testkit.AgentOf("GH-1")+" "+SkillPrompt("judge", "GH-1", nil, runplan.ShipFlow{}, false),
		"{}",
	)
	fx.expects("GH-2", "judge")
	fx.expects("GH-3", "judge")
	fx.f.Script(testkit.WsList,
		unopened, unopened, listing("GH-1", "idle"), // the gate's look, GH-1's open, the look before its judge
		listing("GH-1", "working"), listing("GH-1", "idle"), // GH-1's turn ends on its ask
		listing("GH-1", "idle"), listing("GH-1", "idle", "GH-2", "idle"), // GH-2's open and look
		listing("GH-1", "idle", "GH-2", "working"), listing("GH-1", "idle", "GH-2", "idle"),
		listing("GH-1", "idle", "GH-2", "idle"), all("idle", "GH-1", "GH-2", "GH-3"), // GH-3's open and look
		listing("GH-1", "idle", "GH-2", "idle", "GH-3", "working"), all("idle", "GH-1", "GH-2", "GH-3"))
	fx.onTick[2] = func() { fx.writeRunFor(t, "GH-1", "blocked", "judge", "", "1. Rethink: [proceed | stop]") }

	if err := fx.drive(t); testkit.ExitCode(err) != ui.ExitAsk {
		t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitAsk, fx.out.String())
	}

	fx.assertOrder(t, []string{
		"--create GH-1", testkit.AgentOf("GH-1") + " /f10:judge",
		"--create GH-2", testkit.AgentOf("GH-2") + " /f10:judge",
		"--create GH-3", testkit.AgentOf("GH-3") + " /f10:judge",
	}, "herdr agent prompt", "wt switch")

	out := fx.out.String()
	for _, want := range []string{
		"GH-2: queued, 1 running", "GH-3: queued, 1 running",
		"GH-1:    asked at judge", "GH-2:    done: judge", "GH-3:    done: judge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// GH-3 is still queued when GH-2 opens: the ask freed one slot, not two
	if i, j := strings.Index(out, "GH-2: branch"), strings.Index(out, "GH-3: branch"); i < 0 || j < i ||
		strings.Contains(out[i:j], "GH-3 judge: sent") {
		t.Errorf("GH-3 opened alongside GH-2:\n%s", out)
	}
}
