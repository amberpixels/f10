package drive

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A ship typed by hand into the pane while the drive follows plan, its guard
// stopped on a main that moved: the branch says plan is done and the ship
// needs a catchup, so the drive catches up, resumes ship, and halts nothing.
func TestDriveCatchesUpAShipTypedByHand(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "plan", "ship")
	fx.adoptsGH1()

	catchup := catchupTurn("GH-1")
	resume := "herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 4) --driven"

	fx.f.Script(
		"herdr agent prompt "+testkit.AgentOf("GH-1")+" "+skillPrompt("plan", "GH-1", nil, runplan.ShipFlow{}, false),
		"{}",
	)

	fx.f.Script(catchup, "{}")
	fx.f.Script(resume, "{}")
	fx.f.Fail("git merge-tree --write-tree --no-messages main0 HEAD", shell.Result{Code: 1, Stdout: aOneWay})

	// plan's turn, which the hand-typed ship took over; the catchup's; the resumed ship's
	idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
	fx.f.Script(testkit.WsList, unopened, idle, working, idle, idle, working, idle, idle, working, idle)
	fx.onTick[2] = func() {
		fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
		fx.f.Meta.Seed("GH-1", branchmeta.Landed, "main")
	}
	fx.onTick[4] = func() { restored(fx, t) }
	fx.onTick[6] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	out := fx.out.String()
	for _, want := range []string{"GH-1 plan: done (from the branch)", "GH-1 catchup: done", "GH-1:    done: plan ship"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if !fx.f.Called(resume) {
		t.Errorf("ship was not resumed after the catchup: %q", fx.f.Calls)
	}
}

// An agent gone idle with no report, whose branch has its plan, its own
// commits and an open PR carrying them, has shipped.
func TestDriveTakesAShippedBranchFromGit(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.task("GH-1", tasksource.TaskInfo{Body: "x"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "ship")
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	// the turn ships, its report never written: pushed as it stands,
	// commits of its own, a PR open
	fx.onTick[1] = func() {
		fx.writeRun(t, "running", "implement", "", "")
		fx.f.Script("git rev-parse -q --verify refs/heads/GH-1", "c1")
		fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "c1")
		fx.f.Script("git rev-list --count --no-merges refs/heads/GH-1 --not refs/heads/main", "2")
	}

	fx.in.Pull = func(context.Context, string) (forge.PR, error) { return forge.PR{Status: forge.Open, Number: 7}, nil }

	err := fx.drive(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if out := fx.out.String(); !strings.Contains(out, "GH-1 ship: done (from the branch)") ||
		!strings.Contains(out, "GH-1:    done: ship") || strings.Contains(out, "halted") {
		t.Errorf("output = %s", out)
	}
}

// The attempt rule: an action failing with the state unchanged halts on the
// third failure and lists every attempt; one that fails, then goes through,
// does not halt; one whose state moves on each failure never halts.
func TestDriveHaltsOnlyOnRepeatedFailureWithNoProgress(t *testing.T) {
	stop := func(fx *driveFixture, t *testing.T) { fx.writeRun(t, "failed", "implement", "red verify", "") }

	cases := map[string]struct {
		// turns are what each ship turn leaves, in order
		turns []func(fx *driveFixture, t *testing.T)
		// heads are the branch's head after each turn
		heads []string
		halt  bool
	}{
		"three failures, nothing changed": {
			turns: []func(*driveFixture, *testing.T){stop, stop, stop},
			heads: []string{"c1", "c1", "c1"},
			halt:  true,
		},
		"a failure, then through": {
			turns: []func(*driveFixture, *testing.T){
				stop,
				func(fx *driveFixture, t *testing.T) { fx.writeRun(t, "done", "", "", "") },
			},
			heads: []string{"c1", "c1"},
		},
		"each failure moves the branch": {
			turns: []func(*driveFixture, *testing.T){
				stop, stop, stop,
				func(fx *driveFixture, t *testing.T) { fx.writeRun(t, "done", "", "", "") },
			},
			heads: []string{"c1", "c2", "c3", "c3"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.task("GH-1", tasksource.TaskInfo{Body: "x"}, "")
			fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.expects("GH-1", "ship")

			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			// the open's look and the first turn's, then each retry: the
			// tick that finds the resend, the send's own look, the turn
			looks := []string{unopened, idle, working, idle}

			for range tc.turns[1:] {
				looks = append(looks, idle, idle, working, idle)
			}

			fx.f.Script(testkit.WsList, looks...)

			for i, turn := range tc.turns {
				fx.onTick[2+3*i] = func() {
					turn(fx, t)
					fx.f.Script("git rev-parse -q --verify refs/heads/GH-1", tc.heads[i])
				}
			}

			var halts []ledger.Event

			fx.in.Record = func(_ context.Context, _ string, e ledger.Event) { halts = append(halts, e) }

			err := fx.drive(t)
			out := fx.out.String()

			if !tc.halt {
				if err != nil || strings.Contains(out, "halted") {
					t.Fatalf("err = %v\n%s", err, out)
				}

				return
			}

			if testkit.ExitCode(err) != ui.ExitHalted {
				t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitHalted, out)
			}

			for _, want := range []string{
				"GH-1 halted at ship: red verify",
				"ship failed 3 times in a row with nothing changed",
				"attempt 1 at", "attempt 2 at", "attempt 3 at",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}

			if len(halts) != 1 || len(halts[0].Payload.Val["attempts"].([]string)) != 3 {
				t.Errorf("halts recorded: %+v", halts)
			}
		})
	}
}

// The fingerprint leaves out what moves on every turn, and takes in what
// only progress moves.
func TestTaskStateFingerprint(t *testing.T) {
	base := taskState{Head: "c1", Run: &state.Run{Plan: "done", Ship: "failed", Leaf: "implement"}}

	same := base
	same.Run = &state.Run{Plan: "done", Ship: "failed", Leaf: "implement", Note: "another note"}

	if base.Fingerprint() != same.Fingerprint() {
		t.Error("a new note changed the fingerprint")
	}

	for name, moved := range map[string]taskState{
		"commit":   {Head: "c2", Run: base.Run},
		"review":   {Head: "c1", Reviews: 1, Run: base.Run},
		"pr":       {Head: "c1", Pr: "7", Run: base.Run},
		"run leaf": {Head: "c1", Run: &state.Run{Plan: "done", Ship: "failed", Leaf: "pr"}},
	} {
		if moved.Fingerprint() == base.Fingerprint() {
			t.Errorf("%s left the fingerprint unchanged", name)
		}
	}
}

// shipped reads the last delivering entry, and every review and change judge
// on the way to it.
func TestTaskStateShipped(t *testing.T) {
	pipeline := []string{"judge", "implement", "cleanup", "pr", "review:local", "judge", "pr"}
	running := &state.Run{Ship: "running", Pipeline: pipeline}

	cases := map[string]struct {
		s    taskState
		want bool
	}{
		"no pipeline, PR open and pushed": {
			s:    taskState{Head: "c", Origin: "c", Code: true, Pr: "7"},
			want: true,
		},
		"no pipeline, push behind": {s: taskState{Head: "c", Origin: "b", Code: true, Pr: "7"}},
		"no pipeline, no PR":       {s: taskState{Head: "c", Origin: "c", Code: true}},
		"stopped at the change judge": {
			s: taskState{Head: "c", Origin: "c", Code: true, Pr: "7", Reviews: 1, Run: running},
		},
		"every gate in": {
			s: taskState{
				Head:         "c",
				Origin:       "c",
				Code:         true,
				Pr:           "7",
				Reviews:      1,
				ChangeJudged: true,
				Run:          running,
			},
			want: true,
		},
		"commit flow": {
			s: taskState{
				Head: "c",
				Code: true,
				Run:  &state.Run{Ship: "running", Pipeline: []string{"implement", "commit"}},
			},
			want: true,
		},
	}

	for name, tc := range cases {
		if got := tc.s.Shipped(); got != tc.want {
			t.Errorf("%s: shipped = %v, want %v", name, got, tc.want)
		}
	}

	failed := taskState{Head: "c", Origin: "c", Code: true, Pr: "7", Run: &state.Run{Ship: "failed"}}
	if failed.Has("ship") {
		t.Error("an explicit ship stop read as shipped")
	}
}

// A catchup's attempts compare its fingerprint (target, conflict, red
// command), not the branch or the reason's wording: a catchup that merges
// moves the head every time without settling anything, so the third with the
// same fingerprint halts, and a moved target or a new conflict starts over.
func TestCatchupAttemptsCountTheFingerprint(t *testing.T) {
	in := &DriveInput{
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	}
	r := &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: "GH-1"}}, O: start.OpenedTask{Path: "/code/repo.GH-1"}}

	var out bytes.Buffer

	in.Out = &out

	attempt := func(fp string) *driveResult {
		res, ok := endOf(
			in.AttemptAt(r, "catchup", fp, halted("GH-1", "ship", "GH-1 still conflicts with main")),
		)
		if !ok {
			return nil
		}

		return &res
	}

	same := drivestore.Fingerprint("m1", "a.go", "")
	for i := range 2 {
		if res := attempt(same); res != nil {
			t.Fatalf("attempt %d halted: %+v", i+1, res)
		}
	}

	for _, moved := range []string{drivestore.Fingerprint("m2", "a.go", ""), drivestore.Fingerprint("m1", "b.go", "")} {
		if res := attempt(moved); res != nil {
			t.Fatalf("a moved fingerprint halted: %+v", res)
		}
	}

	for i := range 2 {
		if res := attempt(same); res != nil {
			t.Fatalf("attempt %d after the moved one halted: %+v", i+1, res)
		}
	}

	if res := attempt(same); res == nil || res.Code != ui.ExitHalted || len(res.Attempts) != 3 {
		t.Fatalf("the third same fingerprint in a row did not halt: %+v\n%s", res, out.String())
	}
}

// The retry line counts failures in a row with nothing changed, and says so
// when a moved state starts that count over, so two lines reading "1 of 3"
// never look like one failure printed twice.
func TestAttemptLinesSayWhatTheyCount(t *testing.T) {
	in := &DriveInput{Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	r := &DriveRun{Task: DriveTask{Ref: ref.Ref{ID: "GH-1"}}}

	var out bytes.Buffer

	in.Out = &out

	for _, fp := range []string{"a", "b", "b"} {
		if res := in.AttemptAt(r, "ship", fp, driveResult{Reason: "pr: base not on origin"}); res != nil {
			t.Fatalf("halted early: %+v", res)
		}
	}

	want := "GH-1 ship: failed, 1 of 3 in a row with nothing changed, trying again: pr: base not on origin\n" +
		"GH-1 ship: failed again, but the task moved since the last failure, so the count starts over " +
		"at 1 of 3, trying again: pr: base not on origin\n" +
		"GH-1 ship: failed, 2 of 3 in a row with nothing changed, trying again: pr: base not on origin\n"
	if got := out.String(); got != want {
		t.Errorf("lines =\n%s\nwant\n%s", got, want)
	}
}
