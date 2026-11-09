package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A ship typed by hand into the pane while the drive follows plan, its guard
// stopped on a main that moved: the branch says plan is done and the ship
// needs a catchup, so the drive catches up, resumes ship, and halts nothing.
func TestDriveCatchesUpAShipTypedByHand(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "plan", "ship")
	fx.adoptsGH1()

	catchup := "herdr agent prompt " + agentOf("GH-1") + " /f10:catchup --driven"
	resume := "herdr agent prompt " + agentOf("GH-1") + " /f10:ship GH-1 resume at pr (entry 4) --driven"

	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("plan", "GH-1", nil, shipFlow{}, false), "{}")

	fx.f.script(catchup, "{}")
	fx.f.script(resume, "{}")
	fx.f.fail("git merge-tree --write-tree --no-messages main0 HEAD", shell.Result{Code: 1, Stdout: aOneWay})

	// plan's turn, which the hand-typed ship took over; the catchup's; the resumed ship's
	idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
	fx.f.script(wsList, unopened, idle, working, idle, idle, working, idle, idle, working, idle)
	fx.onTick[2] = func() {
		fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
		fx.f.meta.Seed("GH-1", branchmeta.Landed, "main")
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

	if !fx.f.called(resume) {
		t.Errorf("ship was not resumed after the catchup: %q", fx.f.calls)
	}
}

// An agent gone idle with no report, whose branch has its plan, its own
// commits and an open PR carrying them, has shipped.
func TestDriveTakesAShippedBranchFromGit(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "ship")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	// the turn ships, its report never written: pushed as it stands,
	// commits of its own, a PR open
	fx.onTick[1] = func() {
		fx.writeRun(t, "running", "implement", "", "")
		fx.f.script("git rev-parse -q --verify refs/heads/GH-1", "c1")
		fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-1", "c1")
		fx.f.script("git rev-list --count --no-merges refs/heads/GH-1 --not refs/heads/main", "2")
	}

	fx.in.pull = func(context.Context, string) (forge.PR, error) { return forge.PR{Status: forge.Open, Number: 7}, nil }

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
			fx.task("GH-1", taskInfo{body: "x"}, "")
			fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
			fx.opens("GH-1", "ws:GH-1", "pane:1")
			fx.expects("GH-1", "ship")

			idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
			// the open's look and the first turn's, then each retry: the
			// tick that finds the resend, the send's own look, the turn
			looks := []string{unopened, idle, working, idle}

			for range tc.turns[1:] {
				looks = append(looks, idle, idle, working, idle)
			}

			fx.f.script(wsList, looks...)

			for i, turn := range tc.turns {
				fx.onTick[2+3*i] = func() {
					turn(fx, t)
					fx.f.script("git rev-parse -q --verify refs/heads/GH-1", tc.heads[i])
				}
			}

			var halts []ledger.Event

			fx.in.record = func(_ context.Context, _ string, e ledger.Event) { halts = append(halts, e) }

			err := fx.drive(t)
			out := fx.out.String()

			if !tc.halt {
				if err != nil || strings.Contains(out, "halted") {
					t.Fatalf("err = %v\n%s", err, out)
				}

				return
			}

			if exitCode(err) != ui.ExitHalted {
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
	base := taskState{head: "c1", run: &state.Run{Plan: "done", Ship: "failed", Leaf: "implement"}}

	same := base
	same.run = &state.Run{Plan: "done", Ship: "failed", Leaf: "implement", Note: "another note"}

	if base.fingerprint() != same.fingerprint() {
		t.Error("a new note changed the fingerprint")
	}

	for name, moved := range map[string]taskState{
		"commit":   {head: "c2", run: base.run},
		"review":   {head: "c1", reviews: 1, run: base.run},
		"pr":       {head: "c1", pr: "7", run: base.run},
		"run leaf": {head: "c1", run: &state.Run{Plan: "done", Ship: "failed", Leaf: "pr"}},
	} {
		if moved.fingerprint() == base.fingerprint() {
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
		"no pipeline, PR open and pushed": {s: taskState{head: "c", origin: "c", code: true, pr: "7"}, want: true},
		"no pipeline, push behind":        {s: taskState{head: "c", origin: "b", code: true, pr: "7"}},
		"no pipeline, no PR":              {s: taskState{head: "c", origin: "c", code: true}},
		"stopped at the change judge": {
			s: taskState{head: "c", origin: "c", code: true, pr: "7", reviews: 1, run: running},
		},
		"every gate in": {
			s:    taskState{head: "c", origin: "c", code: true, pr: "7", reviews: 1, changeJudged: true, run: running},
			want: true,
		},
		"commit flow": {
			s: taskState{
				head: "c",
				code: true,
				run:  &state.Run{Ship: "running", Pipeline: []string{"implement", "commit"}},
			},
			want: true,
		},
	}

	for name, tc := range cases {
		if got := tc.s.shipped(); got != tc.want {
			t.Errorf("%s: shipped = %v, want %v", name, got, tc.want)
		}
	}

	failed := taskState{head: "c", origin: "c", code: true, pr: "7", run: &state.Run{Ship: "failed"}}
	if failed.has("ship") {
		t.Error("an explicit ship stop read as shipped")
	}
}

// A catchup's attempts compare the conflict it left, not the branch or the
// reason's wording: a catchup that merges moves the head every time without
// settling anything, so the third with the same conflict halts, and a new
// conflict starts over.
func TestCatchupAttemptsCountTheConflict(t *testing.T) {
	in := &driveInput{now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	r := &driveRun{task: driveTask{ref: ref.Ref{ID: "GH-1"}}, o: openedTask{path: "/code/repo.GH-1"}}

	var out bytes.Buffer

	same := "GH-1 still conflicts with main after a catchup: the same files conflict with main the same way again: a.go"
	for i := range 2 {
		if res := in.catchupAttempt(&out, r, "ship", same, "a.go"); res != nil {
			t.Fatalf("attempt %d halted: %+v", i+1, res)
		}
	}

	if res := in.catchupAttempt(&out, r, "ship", "a new conflict: b.go", "b.go"); res != nil {
		t.Fatalf("a new conflict halted: %+v", res)
	}

	for i := range 2 {
		if res := in.catchupAttempt(&out, r, "ship", same, "a.go"); res != nil {
			t.Fatalf("attempt %d after the new conflict halted: %+v", i+1, res)
		}
	}

	res := in.catchupAttempt(&out, r, "ship", same, "a.go")
	if res == nil || res.code != ui.ExitHalted || len(res.attempts) != 3 {
		t.Fatalf("the third same conflict in a row did not halt: %+v\n%s", res, out.String())
	}

	if last := res.lines[len(res.lines)-1]; !strings.HasPrefix(last, "next: settle it with /f10:catchup") {
		t.Errorf("halt lines = %q", res.lines)
	}
}
