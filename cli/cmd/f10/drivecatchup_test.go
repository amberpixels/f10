package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// binaryCatchupFake is the drive's own catchup with git's integration and
// verify scripted: each call takes the next answer, the last one repeating.
type binaryCatchupFake struct {
	mu        sync.Mutex
	stops     []bool   // integrate: stopped on a conflict
	onStop    func()   // what a stop leaves in the git dir
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
// main0, the marks held in git config the fakes answer: an unset clears.
func (fx *driveFixture) binaryCatchup(t *testing.T, b *binaryCatchupFake, rebaseDir string) {
	t.Helper()

	plan := catchupPlan{branch: "GH-1", base: "main", sha: "main0", found: foundDefault, method: methodRebase}

	fx.in.catchup = &binaryCatchup{
		integrate: func(context.Context, catchupInput, func(catchupPlan) []string) (catchupPlan, bool, error) {
			b.mu.Lock()
			defer b.mu.Unlock()

			stopped := b.next(b.stops, b.integrals)
			b.integrals++

			if stopped && b.onStop != nil {
				b.onStop()
			}

			return plan, stopped, nil
		},
		verify: func(_ context.Context, _, log string) (verifyRun, error) {
			b.mu.Lock()
			defer b.mu.Unlock()

			red := ""
			if b.verifies < len(b.reds) {
				red = b.reds[b.verifies]
			}

			b.verifies++

			if red != "" {
				return verifyRun{failed: red, log: log}, nil
			}

			return verifyRun{}, nil
		},
		base: func(context.Context, catchupInput) (catchupPlan, error) {
			p := plan
			p.method = methodNone

			return p, nil
		},
	}

	fx.in.root = t.TempDir()

	fx.f.script("git rev-parse --git-path rebase-merge", filepath.Join(rebaseDir, "rebase-merge"))
	fx.f.script("git rev-parse --git-path rebase-apply", filepath.Join(rebaseDir, "rebase-apply"))
	fx.f.script("git merge-base --is-ancestor main0 HEAD", "")
	fx.f.script("git diff --name-only --diff-filter=d -z main0...HEAD", "")
	fx.f.script("git diff --name-only --diff-filter=U -z", "a.go")

	for _, key := range []string{cfgMoved, cfgTip} {
		fx.f.script("git config --get branch.GH-1."+key, "")
	}

	landed := "git config --get branch.GH-1." + cfgLanded
	fx.f.script(landed, "main")
	fx.f.script("git config --unset branch.GH-1."+cfgLanded, "")

	prev := gitx.Exec
	gitx.Exec = func(ctx context.Context, dir string, args ...string) (string, error) {
		out, err := prev(ctx, dir, args...)
		if err == nil && strings.Join(args, " ") == "config --unset branch.GH-1."+cfgLanded {
			fx.f.script(landed, "")
		}

		return out, err
	}

	t.Cleanup(func() { gitx.Exec = prev })
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
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+resumeGH1, "{}")
	fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")

	idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
	fx.f.script(wsList, unopened, idle, idle, idle, idle, working, idle)
	fx.onTick[6] = func() { fx.writeRun(t, "done", "", "", "") }

	err := fx.drive(t)
	out := fx.out.String()

	if err != nil || !strings.Contains(out, "GH-1 catchup: GH-1 caught up with main, verify green") ||
		!strings.Contains(out, "GH-1:    done: ship") {
		t.Errorf("err = %v\n%s", err, out)
	}

	if fx.f.called("herdr agent prompt " + agentOf("GH-1") + " /f10:catchup") {
		t.Errorf("the agent was prompted for a clean catchup: %q", fx.f.calls)
	}

	if !fx.f.called("git config --unset branch.GH-1." + cfgLanded) {
		t.Errorf("the mark was not cleared: %q", fx.f.calls)
	}
}

// agent plays GH-1's agent across the drive's ticks: each prompt the drive
// sends shows working for one tick, then turn runs with the prompt and the
// agent rests.
func (fx *driveFixture) agent(t *testing.T, turn func(prompt string)) {
	t.Helper()

	idle, working := listing("GH-1", "idle"), listing("GH-1", "working")
	fx.f.script(wsList, unopened, idle)

	seen, pending := 0, ""

	for tick := 1; tick <= 120; tick++ {
		fx.onTick[tick] = func() {
			if pending != "" {
				turn(pending)
				pending = ""
				fx.f.script(wsList, idle)

				return
			}

			var prompts []string

			for _, c := range fx.f.calls {
				if p, ok := strings.CutPrefix(c, "herdr agent prompt "+agentOf("GH-1")+" "); ok {
					prompts = append(prompts, p)
				}
			}

			if len(prompts) > seen {
				pending = prompts[seen]
				seen++

				fx.f.script(wsList, working)
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
				fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+p, "{}")
			}

			fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")

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

			if !fx.f.called("herdr agent prompt " + agentOf("GH-1") + " " + conflict) {
				t.Errorf("no prompt naming the conflict: %q\n%s", fx.f.calls, out)
			}

			if got := fx.f.called("herdr agent prompt " + agentOf("GH-1") + " " + left); got != leaves {
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

// A rebase no turn moves halts on the third attempt, each attempt listed.
func TestDriveHaltsARebaseNoTurnMoves(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "ship")
	fx.adoptsGH1()

	gitDir := t.TempDir()
	fx.binaryCatchup(t, &binaryCatchupFake{stops: []bool{true}, onStop: func() { startRebase(t, gitDir) }}, gitDir)
	fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" /f10:catchup", "{}")

	for _, p := range []string{
		"/f10:catchup the drive rebased GH-1 onto main (main0) and git stopped on conflicts in a.go: " +
			"settle them in place, keeping both sides, then conclude the rebase --driven",
		"/f10:catchup a rebase onto main0 is still in progress in /code/repo.GH-1 at commit 2 of 3, " +
			"conflicted: a.go: settle it in place, keeping both sides, and conclude it --driven",
	} {
		fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+p, "{}")
	}

	fx.agent(t, func(string) {
		fx.writeShipStop(t, "partial", "pr", 4, catchupNext)
		fx.reports(t, fx.clock)
	})

	err := fx.drive(t)
	out := fx.out.String()

	if exitCode(err) != exitHalted || !strings.Contains(out, "attempt 3 at") ||
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
		filepath.Join(fx.in.root, "catchup", "GH-1.log") +
		": fix it by the branch's intent, verify, and commit the fix --driven"

	for _, p := range []string{red, resumeGH1} {
		fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+p, "{}")
	}

	fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")

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

	if !fx.f.called("herdr agent prompt " + agentOf("GH-1") + " " + red) {
		t.Errorf("no prompt naming the red verify: %q\n%s", fx.f.calls, out)
	}

	if err != nil || !strings.Contains(out, "caught up with main, verify green") ||
		!strings.Contains(out, "GH-1:    done: ship") {
		t.Errorf("err = %v\n%s", err, out)
	}
}

func TestVerifySpecOf(t *testing.T) {
	field := func(origin, value string) *facts.Effective {
		return &facts.Effective{Fields: []facts.Field{{Name: "Verify", Origin: origin, Value: value}}}
	}

	cases := map[string]struct {
		eff  *facts.Effective
		cmds []string
		ok   bool
	}{
		"list": {&facts.Effective{Verify: []facts.VerifyDecl{{Run: "go test ./..."}}}, nil, true},
		"detected": {
			field(facts.OriginDetected, "just lint / just test"),
			[]string{"just lint", "just test"},
			true,
		},
		"init's heading": {
			field("declared (main)", "`just lint` / `just test`"),
			[]string{"just lint", "just test"},
			true,
		},
		"prose heading":   {field("declared (main)", "`just test`; never run `just e2e`"), nil, false},
		"two-line prose":  {field("declared (main)", "`just test`\nnever run e2e"), nil, false},
		"judgment":        {field(facts.OriginJudgment, "go test ./..."), nil, false},
		"nothing at all":  {field(facts.OriginAbsent, ""), nil, false},
		"empty backticks": {field("declared (main)", "`` / `x`"), nil, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			spec, why := verifySpecOf(tc.eff)
			if (why == "") != tc.ok {
				t.Fatalf("runnable = %v (%q), want %v", why == "", why, tc.ok)
			}

			if tc.cmds != nil && strings.Join(spec.cmds, "|") != strings.Join(tc.cmds, "|") {
				t.Errorf("commands = %q, want %q", spec.cmds, tc.cmds)
			}
		})
	}
}

func TestRunVerifyStopsAtTheFirstRedAndLogsIt(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "logs", "v.log")

	res, err := runVerify(t.Context(), dir, []string{"true", "echo broke; exit 3", "touch never"}, log)
	if err != nil || res.failed != "echo broke; exit 3" || res.log != log {
		t.Fatalf("run = %+v, %v", res, err)
	}

	if b, _ := os.ReadFile(log); strings.TrimSpace(string(b)) != "broke" {
		t.Errorf("log = %q", b)
	}

	if _, err := os.Stat(filepath.Join(dir, "never")); err == nil {
		t.Error("a command after the red one ran")
	}

	if res, err := runVerify(t.Context(), dir, []string{"true"}, log); err != nil || res.failed != "" {
		t.Errorf("green run = %+v, %v", res, err)
	}
}

// On a real repo: integrateBranch rebases a branch behind main, the branch
// then holds its base with nothing left, and clearMarks drops the mark.
func TestIntegrateBranchThenNothingLeft(t *testing.T) {
	r := newCatchupRepo(t)
	r.at(before, "config", "branch.FT-1/a."+cfgLanded, "main")

	in := r.input("")

	p, stopped, err := integrateBranch(t.Context(), in, func(catchupPlan) []string { return nil })
	if err != nil || stopped || p.method != methodRebase {
		t.Fatalf("integrate = %+v, %v, %v", p, stopped, err)
	}

	d := &driveInput{main: r.dir, catchup: &binaryCatchup{policy: in, base: resolveCatchup}}
	run := &driveRun{o: openedTask{branch: "FT-1/a", path: r.dir}}

	if left, err := d.catchupLeft(t.Context(), run); err != nil || left != "" {
		t.Fatalf("left = %q, %v, want nothing", left, err)
	}

	base, err := d.catchupBase(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}

	if err := clearMarks(t.Context(), r.dir, "FT-1/a", base); err != nil {
		t.Fatal(err)
	}

	if v, _ := gitx.Config(t.Context(), r.dir, "branch.FT-1/a."+cfgLanded); v != "" {
		t.Errorf("the landed mark survived: %q", v)
	}
}

// A conflict left in a file is what is left once the rebase concluded.
func TestCatchupLeftNamesAMarker(t *testing.T) {
	r := newCatchupRepo(t)
	r.commit(during, "a.go", "<<<<<<< HEAD\nx\n", "a marker committed")

	in := r.input("")
	d := &driveInput{main: r.dir, catchup: &binaryCatchup{policy: in, base: resolveCatchup}}
	run := &driveRun{o: openedTask{branch: "FT-1/a", path: r.dir}}

	if _, _, err := integrateBranch(t.Context(), in, func(catchupPlan) []string { return nil }); err != nil {
		t.Fatal(err)
	}

	if left, err := d.catchupLeft(t.Context(), run); err != nil || !strings.Contains(left, "a.go") {
		t.Errorf("left = %q, %v, want the marker in a.go", left, err)
	}
}
