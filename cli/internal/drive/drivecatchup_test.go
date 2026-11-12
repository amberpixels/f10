package drive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// binaryCatchupFake is the drive's own catchup with git's integration and
// verify scripted: each call takes the next answer, the last one repeating.
type binaryCatchupFake struct {
	mu        sync.Mutex
	stops     []bool   // integrate: stopped on a conflict
	onStop    func()   // what a stop leaves in the git dir
	method    string   // integrate's method, rebase when ""
	reds      []string // verify: the failing command, "" for green
	integrals int
	verifies  int
}

func (b *binaryCatchupFake) next(list []bool, n int) bool {
	if n < len(list) {
		return list[n]
	}

	return list[len(list)-1]
}

// binaryCatchup gives the fixture the drive's own catchup over main at
// main0, the landed mark in the fakes' records.
func (fx *driveFixture) binaryCatchup(t *testing.T, b *binaryCatchupFake, rebaseDir string) {
	t.Helper()

	plan := stack.CatchupPlan{
		Branch: "GH-1",
		Base:   "main",
		SHA:    "main0",
		Found:  stack.FoundDefault,
		Method: stack.MethodRebase,
	}

	fx.in.Catchup = &stack.CatchupEngine{
		Integrate: func(context.Context, stack.CatchupInput, func(stack.CatchupPlan) []string) (stack.CatchupPlan, bool, error) {
			b.mu.Lock()
			defer b.mu.Unlock()

			stopped := b.next(b.stops, b.integrals)
			b.integrals++

			if stopped && b.onStop != nil {
				b.onStop()
			}

			p := plan
			if b.method != "" {
				p.Method = b.method
			}

			return p, stopped, nil
		},
		Verify: func(_ context.Context, _, log string) (stack.VerifyRun, error) {
			b.mu.Lock()
			defer b.mu.Unlock()

			red := ""
			if b.verifies < len(b.reds) {
				red = b.reds[b.verifies]
			}

			b.verifies++

			if red != "" {
				return stack.VerifyRun{Failed: red, Log: log}, nil
			}

			return stack.VerifyRun{}, nil
		},
		Base: func(context.Context, stack.CatchupInput) (stack.CatchupPlan, error) {
			p := plan
			p.Method = stack.MethodNone

			return p, nil
		},
	}

	fx.in.Root = t.TempDir()

	fx.f.Script("git rev-parse --git-path rebase-merge", filepath.Join(rebaseDir, "rebase-merge"))
	fx.f.Script("git rev-parse --git-path rebase-apply", filepath.Join(rebaseDir, "rebase-apply"))
	fx.f.Script("git merge-base --is-ancestor main0 HEAD", "")
	fx.f.Script("git diff --name-only --diff-filter=d -z main0...HEAD", "")
	fx.f.Script("git diff --name-only --diff-filter=U -z", "a.go")

	fx.f.Meta.Seed("GH-1", branchmeta.Landed, "main")
}

// startRebase leaves dir looking like a rebase stopped at commit 2 of 3.
func startRebase(t *testing.T, dir string) {
	t.Helper()

	d := filepath.Join(dir, "rebase-merge")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{"msgnum": "2", "end": "3", "onto": "main0"} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const resumeGH1 = "/f10:ship GH-1 resume at pr (entry 4) --driven"

// A branch behind on files it does not touch is caught up, verified and
// its marks cleared with no agent turn, then ship resumes.
func TestDriveCatchesUpCleanWithNoAgentTurn(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()
	fx.binaryCatchup(t, &binaryCatchupFake{stops: []bool{false}}, t.TempDir())
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+resumeGH1, "{}")

	idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
	fx.f.Script(testkit.WsList, unopened, idle, idle, idle, idle, working, idle)
	fx.onTick[6] = func() { fx.writeRun(t, "done", "", "", "") }

	err := fx.drive(t)
	out := fx.out.String()

	if err != nil || !strings.Contains(out, "GH-1 catchup: GH-1 caught up with main, verify green") ||
		!strings.Contains(out, "GH-1:    done: ship") {
		t.Errorf("err = %v\n%s", err, out)
	}

	if fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " /f10:catchup") {
		t.Errorf("the agent was prompted for a clean catchup: %q", fx.f.Calls)
	}

	if !fx.f.Called("unset branch.GH-1." + string(branchmeta.Landed)) {
		t.Errorf("the mark was not cleared: %q", fx.f.Calls)
	}
}

// agent plays GH-1's agent across the drive's ticks: each prompt the drive
// sends shows working for one tick, then turn runs with the prompt and the
// agent rests.
func (fx *driveFixture) agent(t *testing.T, turn func(prompt string)) {
	t.Helper()

	idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
	fx.f.Script(testkit.WsList, unopened, idle)

	seen, pending := 0, ""

	for tick := 1; tick <= 120; tick++ {
		fx.onTick[tick] = func() {
			if pending != "" {
				turn(pending)
				pending = ""
				fx.f.Script(testkit.WsList, idle)

				return
			}

			var prompts []string

			for _, c := range fx.f.Calls {
				if p, ok := strings.CutPrefix(c, "herdr agent prompt "+testkit.AgentOf("GH-1")+" "); ok {
					prompts = append(prompts, p)
				}
			}

			if len(prompts) > seen {
				pending = prompts[seen]
				seen++

				fx.f.Script(testkit.WsList, working)
			}
		}
	}
}

// A conflicting catchup prompts the agent with the conflicted files and,
// once git shows it settled, verifies and moves on; a rebase the agent left
// in progress is prompted once more, naming what is left, with no halt.
func TestDrivePromptsTheAgentOnlyForAConflict(t *testing.T) {
	conflict := "/f10:catchup the drive rebased GH-1 onto main (main0) and git stopped on conflicts in a.go: " +
		"settle them in place, keeping both sides, then conclude the rebase --driven"
	left := "/f10:catchup a rebase onto main0 is still in progress in /code/repo.GH-1 at commit 2 of 3, " +
		"conflicted: a.go: settle it in place, keeping both sides, and conclude it --driven"

	for name, leaves := range map[string]bool{"settled in one turn": false, "rebase left in progress": true} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.adoptsGH1()

			gitDir := t.TempDir()
			stops := &binaryCatchupFake{stops: []bool{true}, onStop: func() { startRebase(t, gitDir) }}
			fx.binaryCatchup(t, stops, gitDir)
			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)

			for _, p := range []string{conflict, left, resumeGH1} {
				fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+p, "{}")
			}

			catchups := 0
			fx.agent(t, func(prompt string) {
				if prompt == resumeGH1 {
					fx.writeRun(t, "done", "", "", "")

					return
				}

				// the step restores the ship stop it found beside its signal
				catchups++

				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.reports(t, fx.clock)

				if !leaves || catchups >= 2 {
					_ = os.RemoveAll(filepath.Join(gitDir, "rebase-merge"))
				}
			})

			err := fx.drive(t)
			out := fx.out.String()

			if !fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " " + conflict) {
				t.Errorf("no prompt naming the conflict: %q\n%s", fx.f.Calls, out)
			}

			if got := fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " " + left); got != leaves {
				t.Errorf("prompt naming what is left sent = %v, want %v\n%s", got, leaves, out)
			}

			if err != nil || strings.Contains(out, "halted") ||
				!strings.Contains(out, "caught up with main, verify green") ||
				!strings.Contains(out, "GH-1:    done: ship") {
				t.Errorf("err = %v\n%s", err, out)
			}
		})
	}
}

// A turn that concludes the rebase but leaves a marker or uncommitted paths
// is prompted again naming what is left, never integrated past and pushed.
func TestDrivePromptsAgainForWhatATurnLeft(t *testing.T) {
	markers := "git grep -l -I -z -E ^(<<<<<<<|>>>>>>>)( |$) -- a.go"

	cases := map[string]struct {
		leave, settle func(fx *driveFixture)
		left          string
	}{
		"a marker": {
			leave: func(fx *driveFixture) {
				fx.f.Script("git diff --name-only --diff-filter=d -z main0...HEAD", "a.go")
				fx.f.Script(markers, "a.go")
			},
			settle: func(fx *driveFixture) { fx.f.Fail(markers, shell.Result{Code: 1}) },
			left:   "conflict markers left in a.go",
		},
		"uncommitted paths": {
			leave:  func(fx *driveFixture) { fx.f.Script("git status --porcelain", " M a.go") },
			settle: func(fx *driveFixture) { fx.f.Script("git status --porcelain", "") },
			left:   "1 uncommitted paths in /code/repo.GH-1",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.adoptsGH1()

			gitDir := t.TempDir()
			stops := &binaryCatchupFake{stops: []bool{true}, onStop: func() { startRebase(t, gitDir) }}
			fx.binaryCatchup(t, stops, gitDir)
			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)

			again := "/f10:catchup the last catchup turn left it unsettled: " + tc.left +
				": settle it in place, keeping both sides, and commit --driven"

			for _, p := range []string{
				"/f10:catchup the drive rebased GH-1 onto main (main0) and git stopped on conflicts in a.go: " +
					"settle them in place, keeping both sides, then conclude the rebase --driven",
				again, resumeGH1,
			} {
				fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+p, "{}")
			}

			turns := 0
			fx.agent(t, func(prompt string) {
				if prompt == resumeGH1 {
					fx.writeRun(t, "done", "", "", "")

					return
				}

				turns++

				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.reports(t, fx.clock)

				// the first turn concludes the rebase but leaves something behind
				_ = os.RemoveAll(filepath.Join(gitDir, "rebase-merge"))

				if turns == 1 {
					tc.leave(fx)
				} else {
					tc.settle(fx)
				}
			})

			err := fx.drive(t)
			out := fx.out.String()

			if !fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " " + again) {
				t.Errorf("no prompt naming what was left: %q\n%s", fx.f.Calls, out)
			}

			if stops.integrals != 1 {
				t.Errorf("integrated %d times, want once", stops.integrals)
			}

			if err != nil || !strings.Contains(out, "caught up with main, verify green") ||
				!strings.Contains(out, "GH-1:    done: ship") {
				t.Errorf("err = %v\n%s", err, out)
			}
		})
	}
}

// A rebase no turn moves halts on the third attempt, each attempt listed.
func TestDriveHaltsARebaseNoTurnMoves(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()

	gitDir := t.TempDir()
	fx.binaryCatchup(t, &binaryCatchupFake{stops: []bool{true}, onStop: func() { startRebase(t, gitDir) }}, gitDir)
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" /f10:catchup", "{}")

	for _, p := range []string{
		"/f10:catchup the drive rebased GH-1 onto main (main0) and git stopped on conflicts in a.go: " +
			"settle them in place, keeping both sides, then conclude the rebase --driven",
		"/f10:catchup a rebase onto main0 is still in progress in /code/repo.GH-1 at commit 2 of 3, " +
			"conflicted: a.go: settle it in place, keeping both sides, and conclude it --driven",
	} {
		fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+p, "{}")
	}

	fx.agent(t, func(string) {
		fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
		fx.reports(t, fx.clock)
	})

	err := fx.drive(t)
	out := fx.out.String()

	if testkit.ExitCode(err) != ui.ExitHalted || !strings.Contains(out, "attempt 3 at") ||
		!strings.Contains(out, "next: settle it with /f10:catchup in /code/repo.GH-1, then rerun the drive") {
		t.Errorf("err = %v\n%s", err, out)
	}
}

// A clean integration with red verify goes to the agent with the failing
// command; the drive's own verify then decides.
func TestDrivePromptsTheAgentOnRedVerify(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()
	fx.binaryCatchup(t, &binaryCatchupFake{stops: []bool{false}, reds: []string{"just test"}}, t.TempDir())
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)

	red := "/f10:catchup main came in cleanly and verify is red: `just test` failed, its output is in " +
		filepath.Join(fx.in.Root, "catchup", "GH-1.log") +
		": fix it by the branch's intent, verify, and commit the fix --driven"

	for _, p := range []string{red, resumeGH1} {
		fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+p, "{}")
	}

	fx.agent(t, func(prompt string) {
		if prompt == resumeGH1 {
			fx.writeRun(t, "done", "", "", "")

			return
		}

		fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
		fx.reports(t, fx.clock)
	})

	err := fx.drive(t)
	out := fx.out.String()

	if !fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " " + red) {
		t.Errorf("no prompt naming the red verify: %q\n%s", fx.f.Calls, out)
	}

	if err != nil || !strings.Contains(out, "caught up with main, verify green") ||
		!strings.Contains(out, "GH-1:    done: ship") {
		t.Errorf("err = %v\n%s", err, out)
	}
}

// A red verify no turn fixes halts on the third red with the head unchanged.
func TestDriveHaltsARedVerifyNoTurnFixes(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()
	reds := []string{"just test", "just test", "just test"}
	fx.binaryCatchup(t, &binaryCatchupFake{stops: []bool{false}, reds: reds}, t.TempDir())
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
	fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" /f10:catchup main came in cleanly and verify is red: "+
		"`just test` failed, its output is in "+filepath.Join(fx.in.Root, "catchup", "GH-1.log")+
		": fix it by the branch's intent, verify, and commit the fix --driven", "{}")

	fx.agent(t, func(string) {
		fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
		fx.reports(t, fx.clock)
	})

	err := fx.drive(t)
	out := fx.out.String()

	if testkit.ExitCode(err) != ui.ExitHalted || !strings.Contains(out, "attempt 3 at") ||
		!strings.Contains(out, "verify is red after the catchup: just test failed") {
		t.Errorf("err = %v\n%s", err, out)
	}
}

// A red verify still halts on its third red when every other turn leaves the
// tree unsettled: what a turn left counts apart from the red, so the two
// taking turns do not start each other's count over.
func TestDriveHaltsARedVerifyBetweenUnsettledTurns(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()
	reds := []string{"just test", "just test", "just test"}
	fx.binaryCatchup(t, &binaryCatchupFake{stops: []bool{false}, reds: reds}, t.TempDir())
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)

	for _, p := range []string{
		"/f10:catchup main came in cleanly and verify is red: `just test` failed, its output is in " +
			filepath.Join(fx.in.Root, "catchup", "GH-1.log") +
			": fix it by the branch's intent, verify, and commit the fix --driven",
		"/f10:catchup the last catchup turn left it unsettled: 1 uncommitted paths in /code/repo.GH-1" +
			": settle it in place, keeping both sides, and commit --driven",
	} {
		fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+p, "{}")
	}

	turns := 0
	fx.agent(t, func(string) {
		turns++

		fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
		fx.reports(t, fx.clock)

		// each fix turn leaves a path uncommitted, and the turn after commits it
		if turns%2 == 1 {
			fx.f.Script("git status --porcelain", " M a.go")
		} else {
			fx.f.Script("git status --porcelain", "")
		}
	})

	err := fx.drive(t)
	out := fx.out.String()

	if testkit.ExitCode(err) != ui.ExitHalted || !strings.Contains(out, "attempt 3 at") ||
		!strings.Contains(out, "verify is red after the catchup: just test failed") {
		t.Errorf("err = %v\n%s", err, out)
	}
}

// An idle agent on a run still running halts the catchup before git moves.
func TestDriveHaltsACatchupUnderAStalledAgent(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()

	b := &binaryCatchupFake{stops: []bool{false}}
	fx.binaryCatchup(t, b, t.TempDir())
	fx.writeShipStop(t, "running", "pr", 4, "")
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"))

	err := fx.drive(t)
	out := fx.out.String()

	if testkit.ExitCode(err) != ui.ExitHalted || !strings.Contains(out, "is idle while its run says ship") {
		t.Errorf("err = %v\n%s", err, out)
	}

	if b.integrals != 0 {
		t.Errorf("integrated %d times under a stalled agent, want none", b.integrals)
	}
}

// A rerun that finds the base already held, its branch rewritten by an
// earlier drive, never leases a force-push off an origin tip read after it.
func TestDriveNeverForcePushesARewriteItDidNotMake(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()
	fx.binaryCatchup(t, &binaryCatchupFake{stops: []bool{false}, method: stack.MethodNone}, t.TempDir())
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)

	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "p1")
	fx.f.Script("git fetch origin", "")
	fx.f.Fail("git merge-base --is-ancestor p1 HEAD", shell.Result{Code: 1})
	fx.f.Script("git merge-base --is-ancestor p1 refs/heads/GH-1", "")
	fx.f.Script("git merge-base --is-ancestor main0 refs/remotes/origin/main", "")
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"))

	err := fx.drive(t)
	out := fx.out.String()

	if testkit.ExitCode(err) != ui.ExitHalted ||
		!strings.Contains(out, "the drive did not read origin/GH-1 when the catchup went out") {
		t.Errorf("err = %v\n%s", err, out)
	}

	if fx.f.Called("git push") {
		t.Errorf("pushed a rewrite the drive did not make: %q", fx.f.Calls)
	}
}

// A branch origin is ahead of (a commit pushed to the PR, not yet in the
// worktree) is never caught up: the lease is read after that commit landed,
// so a rebase would push over it. The task halts naming the fix.
func TestDriveRefusesACatchupBehindOrigin(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()
	b := &binaryCatchupFake{stops: []bool{false}}
	fx.binaryCatchup(t, b, t.TempDir())
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)

	fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "p2")
	fx.f.Script("git fetch origin", "")
	fx.f.Fail("git merge-base --is-ancestor p2 refs/heads/GH-1", shell.Result{Code: 1})
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"))

	err := fx.drive(t)
	out := fx.out.String()

	if testkit.ExitCode(err) != ui.ExitHalted ||
		!strings.Contains(out, "origin/GH-1 has commits GH-1 lacks") ||
		!strings.Contains(out, "bring origin/GH-1 into /code/repo.GH-1") {
		t.Errorf("err = %v\n%s", err, out)
	}

	if b.integrals != 0 || fx.f.Called("git push") {
		t.Errorf("integrated %d times, calls %q", b.integrals, fx.f.Calls)
	}
}

func TestVerifySpecOf(t *testing.T) {
	field := func(origin facts.Origin, value string) *facts.Effective {
		return &facts.Effective{Fields: []facts.Field{{Name: "Verify", Origin: origin, Value: value}}}
	}

	main := facts.DeclaredIn("main")

	// detection hands the commands over typed; the display Value is never split
	detected := field(facts.OriginOf(facts.OriginDetected), "just lint / just test")
	detected.VerifyCommands = []string{"just lint", "just test"}

	cases := map[string]struct {
		eff  *facts.Effective
		cmds []string
		ok   bool
	}{
		"list":            {&facts.Effective{Verify: []facts.VerifyDecl{{Run: "go test ./..."}}}, nil, true},
		"detected":        {detected, []string{"just lint", "just test"}, true},
		"init's heading":  {field(main, "`just lint` / `just test`"), []string{"just lint", "just test"}, true},
		"prose heading":   {field(main, "`just test`; never run `just e2e`"), nil, false},
		"two-line prose":  {field(main, "`just test`\nnever run e2e"), nil, false},
		"judgment":        {field(facts.OriginOf(facts.OriginJudgment), "go test ./..."), nil, false},
		"nothing at all":  {field(facts.OriginOf(facts.OriginAbsent), ""), nil, false},
		"empty backticks": {field(main, "`` / `x`"), nil, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			spec, why := VerifySpecOf(tc.eff)
			if (why == "") != tc.ok {
				t.Fatalf("runnable = %v (%q), want %v", why == "", why, tc.ok)
			}

			if tc.cmds != nil && strings.Join(spec.Cmds, "|") != strings.Join(tc.cmds, "|") {
				t.Errorf("commands = %q, want %q", spec.Cmds, tc.cmds)
			}
		})
	}
}

// On a real repo: IntegrateBranch rebases a branch behind main, the branch
// then holds its base with nothing left, and ClearMarks drops the mark.
func TestIntegrateBranchThenNothingLeft(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.At(testkit.Before, "config", branchmeta.Landed.For("FT-1/a"), "main")

	in := r.Input("")

	p, stopped, err := stack.IntegrateBranch(t.Context(), in, func(stack.CatchupPlan) []string { return nil })
	if err != nil || stopped || p.Method != stack.MethodRebase {
		t.Fatalf("integrate = %+v, %v, %v", p, stopped, err)
	}

	d := &DriveInput{
		Main:    r.Dir,
		Catchup: &stack.CatchupEngine{Policy: in, Base: stack.ResolveCatchup},
	}
	run := &DriveRun{O: start.OpenedTask{Branch: "FT-1/a", Path: r.Dir}}

	if tree, err := d.catchupLeft(t.Context(), run); err != nil || tree.Left() != "" {
		left := tree.Left()
		t.Fatalf("left = %q, %v, want nothing", left, err)
	}

	base, err := d.catchupBase(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}

	if err := stack.ClearMarks(t.Context(), r.Dir, "FT-1/a", base); err != nil {
		t.Fatal(err)
	}

	if v, _ := gitx.Config(t.Context(), r.Dir, branchmeta.Landed.For("FT-1/a")); v != "" {
		t.Errorf("the landed mark survived: %q", v)
	}
}

// A conflict left in a file is what is left once the rebase concluded.
func TestCatchupLeftNamesAMarker(t *testing.T) {
	r := testkit.NewCatchupRepo(t)
	r.Commit(testkit.During, "a.go", "<<<<<<< HEAD\nx\n", "a marker committed")

	in := r.Input("")
	d := &DriveInput{
		Main:    r.Dir,
		Catchup: &stack.CatchupEngine{Policy: in, Base: stack.ResolveCatchup},
	}
	run := &DriveRun{O: start.OpenedTask{Branch: "FT-1/a", Path: r.Dir}}

	if _, _, err := stack.IntegrateBranch(
		t.Context(),
		in,
		func(stack.CatchupPlan) []string { return nil },
	); err != nil {
		t.Fatal(err)
	}

	if tree, err := d.catchupLeft(t.Context(), run); err != nil || !strings.Contains(tree.Left(), "a.go") {
		left := tree.Left()
		t.Errorf("left = %q, %v, want the marker in a.go", left, err)
	}
}

// A catchup turn that goes idle without its report, leaving what git
// explains, is prompted again naming what is left; it halts only on the third
// attempt with nothing changed, or at once over an open dialog.
func TestDriveSendsAStalledCatchupBack(t *testing.T) {
	conflict := "/f10:catchup the drive rebased GH-1 onto main (main0) and git stopped on conflicts in a.go: " +
		"settle them in place, keeping both sides, then conclude the rebase --driven"
	again := "/f10:catchup the last catchup turn left it unsettled: 1 uncommitted paths in /code/repo.GH-1: " +
		"settle it in place, keeping both sides, and commit --driven"

	cases := map[string]struct {
		settles bool // the second turn commits and reports
		dialog  bool // a permission dialog is open when the turn stalls
		want    string
	}{
		"settled on the second turn": {settles: true, want: "caught up with main, verify green"},
		"never settled":              {want: "attempt 3 at"},
		"under a permission dialog":  {dialog: true, want: "stopped without reporting"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.adoptsGH1()

			gitDir := t.TempDir()
			stops := &binaryCatchupFake{stops: []bool{true}, onStop: func() { startRebase(t, gitDir) }}
			fx.binaryCatchup(t, stops, gitDir)
			fx.writeShipStop(t, "partial", "pr", 4, catchupNext)

			for _, p := range []string{conflict, again, resumeGH1} {
				fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+p, "{}")
			}

			turns := 0
			fx.agent(t, func(prompt string) {
				if prompt == resumeGH1 {
					fx.writeRun(t, "done", "", "", "")

					return
				}

				turns++

				_ = os.RemoveAll(filepath.Join(gitDir, "rebase-merge"))

				if tc.settles && turns == 2 {
					fx.f.Script("git status --porcelain", "")
					fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
					fx.reports(t, fx.clock)

					return
				}

				// the turn ends on a background command, its edit uncommitted
				fx.f.Script("git status --porcelain", " M a.go")

				if tc.dialog {
					wait := `{"reason":"permission","tool":"Bash"}`
					if err := os.WriteFile(state.WaitPath(fx.in.StateDir, "s-GH-1"), []byte(wait), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			})

			err := fx.drive(t)
			out := fx.out.String()

			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}

			if tc.settles != (err == nil) {
				t.Errorf("err = %v\n%s", err, out)
			}

			if got := fx.f.Called("herdr agent prompt " + testkit.AgentOf("GH-1") + " " + again); got == tc.dialog {
				t.Errorf("prompt naming what is left sent = %v, want %v\n%s", got, !tc.dialog, out)
			}
		})
	}
}

// FT-422 through the drive: the catchup turn ends idle on a background go
// test, its edit uncommitted, and reports minutes later. The mark the hook
// left keeps the drive from reading a stall, so nothing is resent.
func TestDriveWaitsOutACatchupParkedOnBackgroundWork(t *testing.T) {
	conflict := "/f10:catchup the drive rebased GH-1 onto main (main0) and git stopped on conflicts in a.go: " +
		"settle them in place, keeping both sides, then conclude the rebase --driven"

	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()

	gitDir := t.TempDir()
	stops := &binaryCatchupFake{stops: []bool{true}, onStop: func() { startRebase(t, gitDir) }}
	fx.binaryCatchup(t, stops, gitDir)
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)

	for _, p := range []string{conflict, resumeGH1} {
		fx.f.Script("herdr agent prompt "+testkit.AgentOf("GH-1")+" "+p, "{}")
	}

	parked := false

	fx.agent(t, func(prompt string) {
		if prompt == resumeGH1 {
			fx.writeRun(t, "done", "", "", "")

			return
		}

		_ = os.RemoveAll(filepath.Join(gitDir, "rebase-merge"))
		fx.f.Script("git status --porcelain", " M a.go")

		mark := state.Background{At: fx.clock, Parked: true}
		if err := state.MarkBackground(fx.in.StateDir, "s-GH-1", mark); err != nil {
			t.Fatal(err)
		}

		parked = true
	})

	// the background job finishes well past the grace; the woken turn commits
	// and reports
	waited := 0

	for tick := 1; tick <= 120; tick++ {
		look := fx.onTick[tick]
		fx.onTick[tick] = func() {
			look()

			if !parked {
				return
			}

			waited++

			if waited == 6 {
				parked = false
				_ = os.Remove(state.BackgroundPath(fx.in.StateDir, "s-GH-1"))
				fx.f.Script("git status --porcelain", "")
				fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
				fx.reports(t, fx.clock)
			}
		}
	}

	err := fx.drive(t)
	out := fx.out.String()

	if err != nil || strings.Contains(out, "trying again") ||
		!strings.Contains(out, "caught up with main, verify green") || !strings.Contains(out, "GH-1:    done: ship") {
		t.Errorf("err = %v\n%s", err, out)
	}

	if waited < 6 {
		t.Errorf("the turn reported after %d looks, want the drive to wait 6", waited)
	}
}

// A pull request retargeted by hand is not caught up at the boundary: a
// catchup with git's base would carry another branch's commits into it, so
// the drive halts naming the fix and sets no mark.
func TestDriveRefusesARetargetedPullRequest(t *testing.T) {
	for name, base := range map[string]string{"retargeted": "feature", "on its base": "main"} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "p1")
			fx.f.Script("git fetch origin", "")
			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", "m2")
			fx.f.Fail("git merge-base --is-ancestor m2 main0", shell.Result{Code: 1})
			fx.f.Fail("git merge-base --is-ancestor m2 refs/heads/GH-1", shell.Result{Code: 1})
			fx.f.Script("set branch.GH-1."+string(branchmeta.Landed)+" main", "")
			fx.in.Pull = func(context.Context, string) (forge.PR, error) {
				return forge.PR{URL: "https://gh/pr/9", Status: forge.Open, Base: base}, nil
			}

			r := &DriveRun{
				Task: DriveTask{Ref: ref.Ref{ID: "GH-1"}},
				O:    start.OpenedTask{Branch: "GH-1", Path: "/code/repo.GH-1"},
			}
			err := fx.in.markBehind(t.Context(), r)
			marked := fx.f.Called("set branch.GH-1." + string(branchmeta.Landed))

			if base == "main" {
				if err != nil || !marked {
					t.Fatalf("err = %v, marked %v: a PR on its base was not marked behind", err, marked)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), "https://gh/pr/9 targets feature, not main") || marked {
				t.Errorf("err = %v, marked %v", err, marked)
			}
		})
	}
}

// A host that does not answer leaves the retarget unknown, so the drive sets
// no mark this poll: a catchup of a PR retargeted meanwhile would carry
// another branch's commits into it. A branch with no PR is marked as before.
func TestDriveMarksNothingBehindAnUnreadPullRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		marked bool
	}{
		"the host fails": {err: errors.New("HTTP 502"), marked: false},
		"no PR":          {err: forge.ErrNotFound, marked: true},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "ship")
			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1", "p1")
			fx.f.Script("git fetch origin", "")
			fx.f.Script("git rev-parse -q --verify refs/remotes/origin/main", "m2")
			fx.f.Fail("git merge-base --is-ancestor m2 main0", shell.Result{Code: 1})
			fx.f.Fail("git merge-base --is-ancestor m2 refs/heads/GH-1", shell.Result{Code: 1})
			fx.f.Script("set branch.GH-1."+string(branchmeta.Landed)+" main", "")
			fx.in.Pull = func(context.Context, string) (forge.PR, error) { return forge.PR{}, tc.err }

			r := &DriveRun{
				Task: DriveTask{Ref: ref.Ref{ID: "GH-1"}},
				O:    start.OpenedTask{Branch: "GH-1", Path: "/code/repo.GH-1"},
			}
			err := fx.in.markBehind(t.Context(), r)
			marked := fx.f.Called("set branch.GH-1." + string(branchmeta.Landed))

			if err != nil || marked != tc.marked {
				t.Fatalf("err = %v, marked %v, want %v\n%s", err, marked, tc.marked, fx.out.String())
			}

			if skipped := strings.Contains(
				fx.out.String(),
				"skipped the target check, reading the PR of GH-1",
			); skipped == tc.marked {
				t.Errorf("skip line printed = %v\n%s", skipped, fx.out.String())
			}
		})
	}
}

// The catchup record outlives the drive's process: kept on the drive record
// as it changes, and read back for the task when the drive resumes.
func TestDriveKeepsTheCatchupRecordForAResume(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.in.Root = t.TempDir()
	fx.in.Rec = &drivestore.Record{Args: []string{"GH-1"}, Tasks: []drivestore.Task{{ID: "GH-1"}}}

	if err := drivestore.Create(fx.in.Root, fx.in.Rec); err != nil {
		t.Fatal(err)
	}

	task := DriveTask{Ref: ref.Ref{ID: "GH-1"}}
	r := &DriveRun{
		Task:        task,
		LastCatchup: &drivestore.Catchup{Target: "m2", Origin: "p1", Base: "main", Integrated: true},
	}
	fx.in.keepCatchup(r)

	back, err := drivestore.Read(fx.in.Root, fx.in.Rec.Number)
	if err != nil {
		t.Fatal(err)
	}

	fx.in.Rec = back

	got := fx.in.savedCatchup(task)
	if got == nil || *got != *r.LastCatchup {
		t.Fatalf("resumed with %+v, want %+v", got, r.LastCatchup)
	}

	// a copy: a change on the resumed run is written only through keepCatchup
	got.Spent = true
	if fx.in.Rec.Task("GH-1").Catchup.Spent {
		t.Error("the resumed record shares the drive record's copy")
	}
}
