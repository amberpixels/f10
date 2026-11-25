package drive

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// orderOf resolves and orders fx's tasks, as Drive does before it creates
// anything.
func orderOf(t *testing.T, fx *driveFixture) ([]DriveTask, []string, error) {
	t.Helper()

	tasks, _, err := fx.in.resolveTasks(t.Context())
	if err != nil {
		return nil, nil, err
	}

	order, _, err := fx.in.graph(t.Context(), tasks)

	var ids []string
	for _, i := range order {
		ids = append(ids, tasks[i].Ref.ID)
	}

	return tasks, ids, err
}

func TestDriveTypedOrder(t *testing.T) {
	t.Run("a chain stacks each entry on the one before, as --after does", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-3,GH-2,GH-1")
		for _, id := range []string{"GH-1", "GH-2", "GH-3"} {
			fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
		}

		tasks, ids, err := orderOf(t, fx)
		if err != nil || strings.Join(ids, " ") != "GH-3 GH-2 GH-1" {
			t.Fatalf("order = %v, %v", ids, err)
		}

		if tasks[1].Base != "GH-3" || tasks[2].Base != "GH-2" || tasks[0].Base != "" {
			t.Errorf("bases = %q %q %q", tasks[0].Base, tasks[1].Base, tasks[2].Base)
		}
	})

	t.Run("a typed base beats the body's After: line", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3@GH-2")
		fx.task("GH-1", tasksource.TaskInfo{Body: "a"}, "")
		fx.task("GH-2", tasksource.TaskInfo{Body: "b"}, "")
		fx.task("GH-3", tasksource.TaskInfo{Body: "After: GH-1"}, "")

		tasks, _, err := orderOf(t, fx)
		if err != nil || tasks[2].Base != "GH-2" || tasks[2].source != baseLine {
			t.Errorf("base = %q, %v; want GH-2 typed", tasks[2].Base, err)
		}
	})

	t.Run("a chain whose entry is a bundle stacks on its lead", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1+GH-2,GH-3")
		for _, id := range []string{"GH-1", "GH-2", "GH-3"} {
			fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
		}

		tasks, ids, err := orderOf(t, fx)
		if err != nil || strings.Join(ids, " ") != "GH-1 GH-3" || tasks[1].Base != "GH-1" {
			t.Errorf("order = %v, base %q, %v", ids, tasks[len(tasks)-1].Base, err)
		}
	})

	t.Run("two bases make a join, ordered after both", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-3@GH-1@GH-2", "GH-1", "GH-2")
		fx.in.Flow.Finishes = true

		for _, id := range []string{"GH-1", "GH-2", "GH-3"} {
			fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
		}

		tasks, ids, err := orderOf(t, fx)
		if err != nil || strings.Join(ids, " ") != "GH-1 GH-2 GH-3" {
			t.Fatalf("order = %v, %v", ids, err)
		}

		if tasks[0].Base != "" || strings.Join(tasks[0].Join, " ") != "GH-1 GH-2" {
			t.Errorf("GH-3 base %q, join %q", tasks[0].Base, tasks[0].Join)
		}
	})

	t.Run("a cycle through @", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1@GH-2", "GH-2@GH-1")
		fx.task("GH-1", tasksource.TaskInfo{Body: "a"}, "")
		fx.task("GH-2", tasksource.TaskInfo{Body: "b"}, "")

		_, _, err := orderOf(t, fx)
		if err == nil || !strings.Contains(err.Error(), "circle: GH-1 after GH-2, GH-2 after GH-1") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("an entry listed twice by two chains", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1,GH-2", "GH-1,GH-3")
		for _, id := range []string{"GH-1", "GH-2", "GH-3"} {
			fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
		}

		if _, _, err := orderOf(t, fx); err == nil || !strings.Contains(err.Error(), "GH-1 is listed twice") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a stack on a base outside the list, not started", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-2@GH-9")
		fx.task("GH-2", tasksource.TaskInfo{Body: "b"}, "")
		fx.task("GH-9", tasksource.TaskInfo{Body: "open"}, "")

		err := fx.drive(t)
		if err == nil || !strings.Contains(err.Error(), "GH-2: the command line names GH-9, which is not in the list") {
			t.Fatalf("err = %v", err)
		}

		if fx.f.Called("wt") || fx.f.Called("herdr") {
			t.Error("something was created before the refusal")
		}
	})
}

func TestDriveRefusesAJoinNothingWouldMerge(t *testing.T) {
	cases := map[string]struct {
		args     []string
		finishes bool
		started  bool // GH-9 has a branch
		want     string
	}{
		"a flow without finish": {
			args: []string{"GH-1", "GH-2", "GH-3@GH-1@GH-2"},
			want: "GH-3 waits for GH-1 and GH-2 to merge, and the default flow does not end in finish",
		},
		"a base outside the list, started and not merged": {
			args: []string{"GH-1", "GH-3@GH-1@GH-9"}, finishes: true, started: true,
			want: "GH-3 waits for GH-9 to merge, which is not in the list and not finished",
		},
		"a base outside the list, not started": {
			args: []string{"GH-1", "GH-3@GH-1@GH-9"}, finishes: true,
			want: "GH-3 waits for GH-9 to merge, which is not in the list and not finished",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, c.args...)
			fx.in.Flow.Finishes = c.finishes

			for _, id := range []string{"GH-1", "GH-2", "GH-3", "GH-9"} {
				fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
			}

			if c.started {
				fx.task("GH-9", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-9"))
			}

			err := fx.drive(t)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}

			if fx.f.Called("wt") || fx.f.Called("herdr") {
				t.Error("something was created before the refusal")
			}
		})
	}

	t.Run("a finished base outside the list is no wait", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "GH-3@GH-1@GH-9")
		fx.in.Flow.Finishes = true

		for _, id := range []string{"GH-1", "GH-3"} {
			fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
		}

		fx.task("GH-9", tasksource.TaskInfo{Closed: true, Stated: true}, "")

		if _, _, err := orderOf(t, fx); err != nil {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("under --plan a join needs no finish", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3@GH-1@GH-2", "--plan")

		for _, id := range []string{"GH-1", "GH-2", "GH-3"} {
			fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
		}

		if _, _, err := orderOf(t, fx); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

// joinRuns is a join on two listed bases, as Drive links them.
func joinRuns() (*DriveRun, *DriveRun, *DriveRun) {
	a := &DriveRun{Task: DriveTask{}}
	a.Task.Ref.ID = "GH-1"
	b := &DriveRun{Task: DriveTask{}}
	b.Task.Ref.ID = "GH-2"
	j := &DriveRun{Task: DriveTask{Join: []string{"GH-1", "GH-2"}}, Join: []*DriveRun{a, b}}
	j.Task.Ref.ID = "GH-3"

	return a, b, j
}

func TestJoinWait(t *testing.T) {
	t.Run("waits until every base merged, printing what is left", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		a, b, j := joinRuns()

		if res := fx.in.joinWait(j); res != nil || j.joinWaits != "GH-1 and GH-2" {
			t.Fatalf("res = %v, waits %q", res, j.joinWaits)
		}

		a.End = &driveResult{Row: drivectl.RowFinished}
		if res := fx.in.joinWait(j); res != nil || j.joinWaits != "GH-2" {
			t.Fatalf("res = %v, waits %q", res, j.joinWaits)
		}

		b.End = &driveResult{Row: drivectl.RowAlreadyFinished}
		if res := fx.in.joinWait(j); res != nil || j.joinWaits != "" {
			t.Fatalf("res = %v, waits %q", res, j.joinWaits)
		}

		out := fx.out.String()
		if !strings.Contains(out, "GH-3: waiting for GH-1 and GH-2 to merge") ||
			!strings.Contains(out, "GH-3: waiting for GH-2 to merge") {
			t.Errorf("out = %q", out)
		}
	})

	t.Run("a halted base halts the join", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		a, _, j := joinRuns()
		a.End = &driveResult{Row: "halted at judge", Code: ui.ExitHalted}

		res := fx.in.joinWait(j)
		if res == nil || res.Code != ui.ExitHalted || res.Row != "halted: base GH-1 halted" {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("a base that ended at its PR halts the join, named", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1")
		a, _, j := joinRuns()
		a.End = &driveResult{Row: "done: judge ship"}

		res := fx.in.joinWait(j)
		if res == nil || !strings.Contains(res.Row, "base GH-1 ended (done: judge ship) without merging") {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("under --plan it opens on both saved plans", func(t *testing.T) {
		fx := newDriveFixture(t, "GH-1", "--plan")
		a, b, j := joinRuns()
		a.Phase, b.Phase = atRunning, atRunning

		if res := fx.in.joinWait(j); res != nil || j.joinWaits == "" {
			t.Fatalf("res = %v, waits %q; want a wait before any plan", res, j.joinWaits)
		}

		for _, id := range []string{"GH-1", "GH-2"} {
			writePlan(t, fx, id)
		}

		if res := fx.in.joinWait(j); res != nil || j.joinWaits != "" {
			t.Errorf("res = %v, waits %q; want released on two plans", res, j.joinWaits)
		}

		// a base that halted after saving its plan still releases
		a.End = &driveResult{Row: "halted at plan", Code: ui.ExitHalted}
		if res := fx.in.joinWait(j); res != nil {
			t.Errorf("res = %+v; want a halted base with a plan to release", res)
		}
	})
}

func writePlan(t *testing.T, fx *driveFixture, id string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(fx.in.Storage.Plan(id)), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(fx.in.Storage.Plan(id), []byte("# plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// the storage root is the package's, so another test must not find it
	t.Cleanup(func() { _ = os.Remove(fx.in.Storage.Plan(id)) })
}

// A plan-only join's plan names both bases and their plans. Any other
// prompt is left alone.
func TestJoinPlanPrompt(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "--plan")
	_, _, j := joinRuns()
	j.Task.Ref.ID = "GH-3"
	writePlan(t, fx, "GH-1")

	p := fx.in.withJoin(j, "plan", skillPrompt("plan", "GH-3", nil, j.Flow, false))
	want := fmt.Sprintf("/f10:plan GH-3 && GH-3 depends on GH-1, whose plan is at %s, and on GH-2, "+
		"whose plan is not written yet (expected at %s) %s",
		fx.in.Storage.Plan("GH-1"), fx.in.Storage.Plan("GH-2"), launch.DrivenMarker)

	if p != want {
		t.Errorf("prompt = %q\nwant     %q", p, want)
	}

	fx.in.Args.PlanOnly = false
	if p := fx.in.withJoin(j, "plan", "/f10:plan GH-3 --driven"); p != "/f10:plan GH-3 --driven" {
		t.Errorf("without --plan the prompt changed: %q", p)
	}
}

func TestJoinStanding(t *testing.T) {
	tasks := []drivestore.Task{
		{ID: "GH-1", Outcome: &drivestore.Outcome{Row: drivectl.RowFinished}},
		{ID: "GH-2-x", Phase: drivestore.PhaseRunning, Skill: "ship"},
		{ID: "GH-3", Join: []string{"GH-1", "GH-2"}, Phase: drivestore.PhasePending},
	}

	if got := taskStanding(tasks[2], true, tasks, nil); got != "waiting for GH-2 to merge" {
		t.Errorf("standing = %q", got)
	}

	tasks[1].Outcome = &drivestore.Outcome{Row: drivectl.RowFinished}
	if got := taskStanding(tasks[2], true, tasks, nil); got != "not reached" {
		t.Errorf("standing once both merged = %q", got)
	}
}

// GH-3 waits for GH-1 and GH-2 to finish, then opens with no base task, so
// it branches from the default branch, its bases recorded on its branch.
func TestDriveOpensAJoinAfterItsBasesFinish(t *testing.T) {
	ids := []string{"GH-1", "GH-2", "GH-3"}
	fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3@GH-1@GH-2", "judge", "finish")
	fx.in.Flow.Finishes = true

	var pairs []string

	for i, id := range ids {
		fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
		pairs = append(pairs, id, "/code/repo."+id)
		fx.opens(id, "ws:"+id, fmt.Sprintf("pane:%d", i))
		fx.expects(id, "judge")
	}

	fx.f.Script(testkit.Worktrees, testkit.Porcelain(pairs...))
	fx.mergeable(ids...)

	fx.in.Finish = func(_ context.Context, _ io.Writer, req DriveFinish) error {
		fx.f.Calls = append(fx.f.Calls, "finish "+req.Task.ID)
		fx.infos[req.Task.ID] = tasksource.TaskInfo{Closed: true}

		return nil
	}

	fx.f.Script(testkit.WsList,
		unopened, all("idle", ids...), unopened, all("idle", ids...), // GH-1's and GH-2's opens and looks
		all("working", ids...), all("idle", ids...), // their turns, then they finish
		unopened, all("idle", ids...), // GH-3's open, then the look before its judge
		all("working", ids...), all("idle", ids...))

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	fx.assertOrder(t, []string{
		testkit.AgentOf("GH-1") + " /f10:judge",
		testkit.AgentOf("GH-2") + " /f10:judge",
		"finish GH-1",
		"finish GH-2",
		testkit.AgentOf("GH-3") + " /f10:judge",
		"finish GH-3",
	}, "herdr agent prompt", "finish")

	if !strings.Contains(fx.out.String(), "GH-3: waiting for GH-1 and GH-2 to merge") {
		t.Errorf("no wait line:\n%s", fx.out.String())
	}

	if fx.f.Meta.Touched("GH-3", branchmeta.After) {
		t.Error("the join recorded a base task: it would stack on one base")
	}

	if !fx.f.Meta.Wrote("GH-3", branchmeta.Join, "GH-1 GH-2") {
		t.Errorf("the join's bases were not recorded: %+v", fx.f.Meta.Writes())
	}
}

// The batch's record keeps the order: each task's bases, and the command
// line a resume parses again, which gives the same bases back.
func TestDriveRecordKeepsTheOrder(t *testing.T) {
	fx := newDriveFixture(t, "GH-1,GH-2", "GH-3@GH-1@GH-2")
	fx.in.Root = t.TempDir()
	fx.in.Flow.Finishes = true

	for _, id := range []string{"GH-1", "GH-2", "GH-3"} {
		fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
	}

	tasks, _, err := orderOf(t, fx)
	if err != nil {
		t.Fatal(err)
	}

	if err := fx.in.openRecord(tasks); err != nil {
		t.Fatal(err)
	}

	defer fx.in.releaseAll()

	rec, err := drivestore.Read(fx.in.Root, fx.in.Rec.Number)
	if err != nil {
		t.Fatal(err)
	}

	if rec.Tasks[1].After != "GH-1" || strings.Join(rec.Tasks[2].Join, " ") != "GH-1 GH-2" {
		t.Errorf("record tasks = %+v", rec.Tasks)
	}

	again, err := drivectl.ParseDriveArgs(rec.Args)
	if err != nil || fmt.Sprint(again.Bases) != fmt.Sprint(fx.in.Args.Bases) {
		t.Errorf("resumed bases = %v, %v; want %v", again.Bases, err, fx.in.Args.Bases)
	}
}

// A base on an ask keeps its join waiting: the batch resumes onto both once
// it is answered, as it does for a stack.
func TestJoinWaitsOnABasesAsk(t *testing.T) {
	fx := newDriveFixture(t, "GH-1")
	a, _, j := joinRuns()
	a.End = &driveResult{Row: "asked at plan", Code: ui.ExitAsk}

	if res := fx.in.joinWait(j); res != nil || j.joinWaits != "GH-1 and GH-2" {
		t.Errorf("res = %+v, waits %q; want both still waited for", res, j.joinWaits)
	}
}

// A join started under --plan has a branch while its bases are unmerged, so
// starting it alone later still refuses an unlisted base that has not merged.
func TestDriveRefusesAStartedJoinWhoseBasesHaveNotMerged(t *testing.T) {
	fx := newDriveFixture(t, "GH-3")
	fx.in.Flow.Finishes = true

	fx.task("GH-3", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-3"))
	fx.f.Meta.Seed("GH-3", branchmeta.Join, "GH-1 GH-2")
	fx.task("GH-1", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-1"))
	fx.task("GH-2", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-2"))

	err := fx.drive(t)
	if err == nil || !strings.Contains(err.Error(), "GH-3 waits for GH-1 to merge, which is not in the list") {
		t.Fatalf("err = %v", err)
	}
}

// Under --plan a join's row names the bases whose plan is not saved.
func TestJoinStandingUnderPlan(t *testing.T) {
	tasks := []drivestore.Task{
		{ID: "GH-1", Phase: drivestore.PhaseRunning, Skill: "plan"},
		{ID: "GH-2", Phase: drivestore.PhaseRunning, Skill: "plan"},
		{ID: "GH-3", Join: []string{"GH-1", "GH-2"}, Phase: drivestore.PhasePending},
	}
	planned := func(id string) bool { return id == "GH-1" }

	if got := taskStanding(tasks[2], true, tasks, planned); got != "waiting for GH-2 to save a plan" {
		t.Errorf("standing = %q", got)
	}
}

// settled resolves and settles fx's tasks, with the graph's notes.
func settled(t *testing.T, fx *driveFixture) ([]DriveTask, []string) {
	t.Helper()

	tasks, _, err := fx.in.resolveTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	_, notes, err := fx.in.graph(t.Context(), tasks)
	if err != nil {
		t.Fatal(err)
	}

	return tasks, notes
}

// GH-3 was started stacked on GH-1, then listed as a join on GH-1 and GH-2.
// Its branch's record wins, so it stays a stack: one shape, and one note.
func TestBranchRecordWinsOverATypedJoin(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3@GH-1@GH-2")
	fx.in.Flow.Finishes = true

	fx.task("GH-1", tasksource.TaskInfo{Body: "x"}, "")
	fx.task("GH-2", tasksource.TaskInfo{Body: "x"}, "")
	fx.task("GH-3", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-3"))
	fx.f.Meta.Seed("GH-3", branchmeta.After, "GH-1")

	tasks, notes := settled(t, fx)

	if tasks[2].Base != "GH-1" || len(tasks[2].Join) != 0 {
		t.Errorf("GH-3 base %q, join %v; want the recorded stack alone", tasks[2].Base, tasks[2].Join)
	}

	want := "GH-3: its branch records after GH-1 and the command line names GH-1 and GH-2; the branch wins"
	if len(notes) != 1 || notes[0] != want {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// A recorded join wins the same way over a typed stack.
func TestBranchJoinWinsOverATypedStack(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "GH-2", "GH-3@GH-1")
	fx.in.Flow.Finishes = true

	fx.task("GH-1", tasksource.TaskInfo{Body: "x"}, "")
	fx.task("GH-2", tasksource.TaskInfo{Body: "x"}, "")
	fx.task("GH-3", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-3"))
	fx.f.Meta.Seed("GH-3", branchmeta.Join, "GH-1 GH-2")

	tasks, notes := settled(t, fx)

	if tasks[2].Base != "" || strings.Join(tasks[2].Join, " ") != "GH-1 GH-2" {
		t.Errorf("GH-3 base %q, join %v; want the recorded join alone", tasks[2].Base, tasks[2].Join)
	}

	want := "GH-3: its branch records after GH-1 and GH-2 and the command line names GH-1; the branch wins"
	if len(notes) != 1 || notes[0] != want {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// --after restacks the one task it names, so it wins over the branch's
// record, and the batch waits on the base the branch is then stacked on.
func TestAfterFlagWinsOverTheBranchRecord(t *testing.T) {
	fx := newDriveFixture(t, "GH-2", "--after", "GH-9")

	fx.task("GH-2", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-2"))
	fx.task("GH-9", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-9"))
	fx.f.Meta.Seed("GH-2", branchmeta.After, "GH-1")

	tasks, notes := settled(t, fx)

	if tasks[0].Base != "GH-9" || tasks[0].source != baseFlag || len(notes) != 0 {
		t.Errorf("base %q, source %v, notes %q; want GH-9 from the flag", tasks[0].Base, tasks[0].source, notes)
	}
}

// A bundle's rider named as a join's base is waited for as the bundle, by
// its lead, in the task, in the record and in the record's wait.
func TestJoinOnARiderWaitsForItsBundle(t *testing.T) {
	fx := newDriveFixture(t, "GH-1+GH-2", "GH-4", "GH-3@GH-2@GH-4")
	fx.in.Root = t.TempDir()
	fx.in.Flow.Finishes = true

	for _, id := range []string{"GH-1", "GH-2", "GH-3", "GH-4"} {
		fx.task(id, tasksource.TaskInfo{Body: "x"}, "")
	}

	tasks, _ := settled(t, fx)

	if got := strings.Join(tasks[2].Join, " "); got != "GH-1 GH-4" {
		t.Fatalf("join = %q, want the bundle's lead and GH-4", got)
	}

	if err := fx.in.openRecord(tasks); err != nil {
		t.Fatal(err)
	}

	defer fx.in.releaseAll()

	rec, err := drivestore.Read(fx.in.Root, fx.in.Rec.Number)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(drivectl.JoinWaits(rec.Tasks[2], rec.Tasks, nil), " "); got != "GH-1 GH-4" {
		t.Errorf("the record waits for %q, want GH-1 GH-4", got)
	}
}

// Under --plan a base in the batch is through once it has opened and its
// plan is saved. A plan saved from the main checkout before the base opened
// does not release the join, and the record's row says what the tick says.
func TestTickAndRecordAgreeOnAPlannedBaseNotOpened(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "--plan")
	a, b, j := joinRuns()
	b.Phase = atRunning

	for _, id := range []string{"GH-1", "GH-2"} {
		writePlan(t, fx, id)
	}

	// GH-1 has a plan and has not opened, GH-2 has both
	if res := fx.in.joinWait(j); res != nil || j.joinWaits != "GH-1" {
		t.Fatalf("the tick waits for %q, %v; want GH-1", j.joinWaits, res)
	}

	tasks := []drivestore.Task{
		{ID: "GH-1", Phase: a.Phase.recorded()},
		{ID: "GH-2", Phase: b.Phase.recorded(), Opened: true, Skill: "plan"},
		{ID: "GH-3", Join: []string{"GH-1", "GH-2"}, Phase: drivestore.PhasePending},
	}

	if got := taskStanding(tasks[2], true, tasks, fx.in.Storage.PlanLive); got != "waiting for GH-1 to save a plan" {
		t.Errorf("the record's row = %q, want the tick's wait on GH-1", got)
	}
}

func TestBaseThrough(t *testing.T) {
	for _, c := range []struct {
		name     string
		b        drivectl.BaseStanding
		planOnly bool
		want     bool
	}{
		{"merged", drivectl.BaseStanding{Finished: true, Listed: true}, false, true},
		{"merged under --plan", drivectl.BaseStanding{Finished: true}, true, true},
		{"open, a plan saved, no --plan", drivectl.BaseStanding{Listed: true, Opened: true, Planned: true}, false, false},
		{"listed, opened and planned", drivectl.BaseStanding{Listed: true, Opened: true, Planned: true}, true, true},
		{"listed, planned, not opened", drivectl.BaseStanding{Listed: true, Planned: true}, true, false},
		{"listed, opened, no plan", drivectl.BaseStanding{Listed: true, Opened: true}, true, false},
		{"outside the batch, planned", drivectl.BaseStanding{Planned: true}, true, true},
		{"outside the batch, nothing", drivectl.BaseStanding{}, true, false},
	} {
		if got := c.b.Through(c.planOnly); got != c.want {
			t.Errorf("%s: through = %v, want %v", c.name, got, c.want)
		}
	}
}
