package drive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/finish/finishtest"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
)

// undrivenTask is an undriven task with an open PR on main, its agent idle.
func undrivenTask() WatchedTask {
	return WatchedTask{
		ID: "GH-1", Branch: "GH-1/x", Path: "/code/repo.GH-1", LocalSHA: "p1",
		Pr:    &forge.PR{URL: "https://gh/pr/9", Status: forge.Open, Base: "main"},
		Agent: agentctl.StatusIdle, Listed: true, Ws: "ws-1",
	}
}

// scriptTarget scripts what a pass reads after its fetch for GH-1/x on
// origin at p1, with origin/main at m2 past the local main at main0.
func scriptTarget(fx *finishtest.JanitorFixture) {
	f := fx.F
	f.Script("git fetch origin", "")
	f.Script("git rev-parse -q --verify refs/remotes/origin/GH-1/x", "p1")
	f.Script("git rev-parse -q --verify refs/heads/main", "main0")
	f.Script("git rev-parse -q --verify refs/remotes/origin/main", "m2")
	f.Fail("git merge-base --is-ancestor m2 main0", shell.Result{Code: 1})
	f.Script("git rev-parse -q --verify MERGE_HEAD", "")
	f.Script("git rev-parse --git-path rebase-merge", "/nonexistent/rebase-merge")
	f.Script("git status --porcelain", "")
	f.Fail("git merge-base --is-ancestor m2 refs/heads/GH-1/x", shell.Result{Code: 1})
	f.Script("git rev-parse -q --verify HEAD", "p1")
	// what a settle reads: the target held, no conflict marker left
	f.Script("git merge-base --is-ancestor m2 HEAD", "")
	f.Script("git diff --name-only --diff-filter=d -z m2...HEAD", "")
	// what a catchup going out records: no conflict with the target
	f.Script("git merge-tree --write-tree --no-messages m2 HEAD", "tree")
}

// watchEngine is the watcher's engine with git's integration scripted: a
// rebase onto origin/main at m2, stopped on a conflict when stopped says so.
func watchEngine(stopped bool) *stack.CatchupEngine {
	return &stack.CatchupEngine{
		AgentVerifies: "the watcher leaves verify to the agent",
		Integrate: func(context.Context, stack.CatchupInput, func(stack.CatchupPlan) []string) (stack.CatchupPlan, bool, error) {
			return stack.CatchupPlan{
				Branch: "GH-1/x",
				Base:   "origin/main",
				SHA:    "m2",
				Method: stack.MethodRebase,
			}, stopped, nil
		},
	}
}

// A pass under watch_catchup: on, as WatchPass runs it after collecting.
func catchupPass(
	t *testing.T,
	in WatchInput,
	tasks []WatchedTask,
	store map[string]WatchReport,
) collected {
	t.Helper()

	var out collected

	in.Out = &out
	if in.Engine == nil {
		in.Engine = watchEngine(false)
	}

	if in.Now == nil {
		in.Now = func() time.Time { return watchNow }
	}

	if in.Catchup {
		in.ReadTargets(t.Context(), tasks, store)
	}

	in.Reconcile(t.Context(), tasks, store)

	return out
}

func TestWatchCatchesUpAnOpenPR(t *testing.T) {
	const mark = "set branch.GH-1/x." + string(branchmeta.Landed) + " main"

	cases := map[string]struct {
		key    bool
		task   func(*WatchedTask)
		script func(*finishtest.JanitorFixture)
		lease  bool // a drive holds the task
		prompt bool // a catchup went out
		kind   string
		warned string
	}{
		"key off":             {task: func(*WatchedTask) {}},
		"behind, idle, clean": {key: true, prompt: true, kind: WatchCatchup},
		"agent working":       {key: true, task: func(w *WatchedTask) { w.Agent = agentctl.StatusWorking }},
		"no listing":          {key: true, task: func(w *WatchedTask) { w.Listed = false; w.Agent = "" }},
		"waiting on an ask": {key: true, task: func(w *WatchedTask) {
			w.Run = &state.Run{Ship: "blocked", Leaf: "judge", Ask: "1. Go on? [yes | no]", Updated: watchNow}
		}, kind: WatchStopped},
		"tree dirty": {key: true, script: func(fx *finishtest.JanitorFixture) {
			fx.F.Script("git status --porcelain", " M a.go")
		}},
		"merge in progress": {key: true, script: func(fx *finishtest.JanitorFixture) {
			fx.F.Script("git rev-parse -q --verify MERGE_HEAD", "mh")
		}},
		"already an ancestor": {key: true, script: func(fx *finishtest.JanitorFixture) {
			fx.F.Script("git merge-base --is-ancestor m2 refs/heads/GH-1/x", "")
		}},
		// a mark a finish or a ship guard left is a catchup nobody else sends
		"already marked": {key: true, script: func(fx *finishtest.JanitorFixture) {
			fx.F.Meta.Seed("GH-1/x", branchmeta.Landed, "main")
		}, prompt: true, kind: WatchCatchup},
		"diverged from origin": {
			key:  true,
			task: func(w *WatchedTask) { w.LocalSHA = "h9" },
			kind: WatchBehind,
		},
		"not on origin": {key: true, script: func(fx *finishtest.JanitorFixture) {
			fx.F.Script("git rev-parse -q --verify refs/remotes/origin/GH-1/x", "")
		}},
		"no workspace": {
			key:  true,
			task: func(w *WatchedTask) { w.Ws, w.Agent = "", "" },
			kind: WatchBehind,
		},
		"retargeted": {
			key:  true,
			task: func(w *WatchedTask) { w.Pr.Base = "feature" },
			kind: WatchBehind,
		},
		"closed": {
			key:  true,
			task: func(w *WatchedTask) { w.Pr.Status = forge.Closed },
			kind: WatchClosed,
		},
		"fetch fails": {key: true, script: func(fx *finishtest.JanitorFixture) {
			fx.F.Fail("git fetch origin", shell.Result{Code: 128, Stderr: "no route to host"})
		}, warned: "no route to host"},
		"lease held":  {key: true, lease: true},
		"handed back": {key: true, task: func(w *WatchedTask) { w.Off = true }},
		"owned":       {key: true, task: func(w *WatchedTask) { w.Owner = "drive 2" }},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := finishtest.NewJanitorFixture(t)
			scriptTarget(fx)

			if tc.script != nil {
				tc.script(fx)
			}

			task := undrivenTask()
			if tc.task != nil {
				tc.task(&task)
			}

			root := t.TempDir()

			if tc.lease {
				lease, err := drivestore.Acquire(root, "GH-1", 2)
				if err != nil {
					t.Fatal(err)
				}

				defer lease.Release()
			}

			var (
				prompts []string
				warned  []string
			)

			in := WatchInput{
				Main: fx.In.Main, Root: root, Mode: "report", Catchup: tc.key,
				Prompt: func(_ context.Context, _ WatchedTask, text string) error {
					prompts = append(prompts, text)

					return nil
				},
				Warn: func(line string) { warned = append(warned, line) },
			}

			store := map[string]WatchReport{}
			out := catchupPass(t, in, []WatchedTask{task}, store)

			kind := ""
			if len(out) > 0 {
				kind = out[0].Kind
			}

			if kind != tc.kind {
				t.Errorf("reported %q, want %q: %+v", kind, tc.kind, out)
			}

			if (len(prompts) > 0) != tc.prompt || fx.F.Called(mark) != tc.prompt {
				t.Errorf("prompted %v (marked %v), want %v", prompts, fx.F.Called(mark), tc.prompt)
			}

			if tc.prompt {
				// the watcher integrated, so the turn is verify, never a whole catchup
				if !strings.Contains(prompts[0], "/f10:catchup --driven origin/main came in cleanly") ||
					!strings.Contains(prompts[0], "sent by the watcher") {
					t.Errorf("prompt = %q", prompts[0])
				}

				rec := store["GH-1"].Catchup
				if rec == nil || rec.Target != "m2" || rec.Head != "p1" || rec.Origin != "p1" ||
					rec.Base != "origin/main" {
					t.Errorf("record = %+v", rec)
				}
			} else if store["GH-1"].Catchup != nil {
				t.Errorf("a record was stored: %+v", store["GH-1"].Catchup)
			}

			if !tc.key ||
				tc.task != nil && (task.Off || task.Owner != "" || !task.Listed || WaitingOnAsk(task.Run)) {
				if fx.F.Called("git fetch origin") {
					t.Error("origin was fetched")
				}
			}

			if tc.warned != "" && (len(warned) != 1 || !strings.Contains(warned[0], tc.warned)) {
				t.Errorf("warned = %q, want %q", warned, tc.warned)
			}

			// the same pass again tells nothing new and sends nothing twice;
			// a sent catchup has its agent working on it
			if tc.prompt {
				task.Agent = agentctl.StatusWorking
			}

			out = catchupPass(t, in, []WatchedTask{task}, store)
			if len(out) != 0 || len(prompts) > 1 {
				t.Errorf("a second pass reported %+v and prompted %d times", out, len(prompts))
			}
		})
	}
}

// A base that is another task's branch marks f10-base-moved with origin's
// head of it, what the stacked request targets; a base origin lacks waits.
func TestWatchCatchesUpAStackedPR(t *testing.T) {
	for _, onOrigin := range []bool{true, false} {
		fx := finishtest.NewJanitorFixture(t)
		scriptTarget(fx)
		fx.F.Meta.Seed("GH-1/x", branchmeta.AfterBranch, "GH-9/y")
		fx.F.Script("git rev-parse -q --verify refs/heads/GH-9/y", "b2")
		fx.F.Fail("git merge-base --is-ancestor b2 refs/heads/GH-1/x", shell.Result{Code: 1})
		fx.F.Script("git merge-tree --write-tree --no-messages b2 HEAD", "tree")

		if onOrigin {
			fx.F.Script("git rev-parse -q --verify refs/remotes/origin/GH-9/y", "b2")
			fx.F.Script("git merge-base --is-ancestor b2 refs/remotes/origin/GH-9/y", "")
		} else {
			fx.F.Script("git rev-parse -q --verify refs/remotes/origin/GH-9/y", "")
			fx.F.Fail("git merge-base --is-ancestor b2 refs/remotes/origin/GH-9/y", shell.Result{Code: 128})
		}

		task := undrivenTask()
		task.Pr.Base = "GH-9/y"

		prompts := 0
		in := WatchInput{
			Main: fx.In.Main, Root: t.TempDir(), Mode: "report", Catchup: true,
			Prompt: func(context.Context, WatchedTask, string) error { prompts++; return nil },
		}

		store := map[string]WatchReport{}
		out := catchupPass(t, in, []WatchedTask{task}, store)
		marked := fx.F.Called("set branch.GH-1/x." + string(branchmeta.Moved) + " b2")

		want := map[bool]int{true: 1, false: 0}[onOrigin]
		if prompts != want || marked != onOrigin || len(out) != want {
			t.Errorf("on origin %v: prompted %d, marked %v, reported %+v", onOrigin, prompts, marked, out)
		}

		if rec := store["GH-1"].Catchup; onOrigin && (rec == nil || rec.Target != "b2" || rec.Base != "GH-9/y") {
			t.Errorf("record = %+v", rec)
		}
	}
}

// An agent the backend cannot find is told once, the mark taken back and
// the branch put back where it was.
func TestWatchReportsAnAgentItCannotPrompt(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	scriptTarget(fx)
	fx.F.Script("git reset --hard p1", "")

	in := WatchInput{
		Main: fx.In.Main, Root: t.TempDir(), Mode: "report", Catchup: true,
		Prompt: func(context.Context, WatchedTask, string) error { return agentctl.ErrAgentNotFound },
	}

	store := map[string]WatchReport{}
	out := catchupPass(t, in, []WatchedTask{undrivenTask()}, store)

	if len(out) != 1 || out[0].Kind != WatchBehind || !strings.Contains(out[0].Text, "not found") ||
		!fx.F.Called("unset branch.GH-1/x."+string(branchmeta.Landed)) || store["GH-1"].Catchup != nil ||
		!fx.F.Called("git reset --hard p1") {
		t.Errorf("reported %+v, unset %v, record %+v", out, fx.F.Called("git config --unset"), store["GH-1"].Catchup)
	}
}

func TestWatchSettlesASentCatchup(t *testing.T) {
	const (
		plain = "git push origin HEAD:refs/heads/GH-1/x"
		lease = "git push --force-with-lease=refs/heads/GH-1/x:p1 origin HEAD:refs/heads/GH-1/x"
	)

	cases := map[string]struct {
		script func(*finishtest.JanitorFixture)
		task   func(*WatchedTask)
		held   bool
		push   string // the push expected, "" for none
		kind   string
		kept   bool // the record stays
		failed bool // and is marked failed
	}{
		"ended clean, merged": {push: plain, kind: WatchCatchup},
		"ended clean, rebased": {script: func(fx *finishtest.JanitorFixture) {
			fx.F.Fail("git merge-base --is-ancestor p1 HEAD", shell.Result{Code: 1})
		}, push: lease, kind: WatchCatchup},
		"head equals origin": {script: func(fx *finishtest.JanitorFixture) {
			fx.F.Script("git rev-parse -q --verify HEAD", "p1")
		}},
		"agent still working": {task: func(w *WatchedTask) { w.Agent = agentctl.StatusWorking }, kept: true},
		// the turn ended on a background verify: the mark is still set, and the
		// catchup is not failed under an agent that will come back to it
		"agent on background work": {script: func(fx *finishtest.JanitorFixture) {
			fx.F.Meta.Seed("GH-1/x", branchmeta.Landed, "main")
		}, task: func(w *WatchedTask) { w.Background = watchNow.Add(-time.Minute) }, kept: true},
		// the mark outlives background work that already ended, so a catchup
		// git shows clean is pushed under it
		"ended clean on background work": {
			task: func(w *WatchedTask) { w.Background = watchNow.Add(-time.Minute) },
			push: plain, kind: WatchCatchup,
		},
		// past the bound the mark says nothing, and what is left is a failure
		"background work past its bound": {script: func(fx *finishtest.JanitorFixture) {
			fx.F.Meta.Seed("GH-1/x", branchmeta.Landed, "main")
		}, task: func(w *WatchedTask) {
			w.Background = watchNow.Add(-stack.VerifyLimit)
		}, kind: WatchCatchup, kept: true, failed: true},
		"mark still set": {script: func(fx *finishtest.JanitorFixture) {
			fx.F.Meta.Seed("GH-1/x", branchmeta.Landed, "main")
		}, kind: WatchCatchup, kept: true, failed: true},
		"rebase left open": {script: func(fx *finishtest.JanitorFixture) {
			fx.F.Script("git rev-parse --git-path rebase-merge", fx.F.T.TempDir())
		}, kind: WatchCatchup, kept: true, failed: true},
		"lease held": {held: true},
		"push rejected": {script: func(fx *finishtest.JanitorFixture) {
			fx.F.Fail(plain, shell.Result{Code: 1, Stderr: "rejected"})
		}, push: plain, kind: WatchCatchup, kept: true},
		// a commit pushed to the request meanwhile is never overwritten
		"origin moved meanwhile": {script: func(fx *finishtest.JanitorFixture) {
			fx.F.Script("git rev-parse -q --verify refs/remotes/origin/GH-1/x", "p2")
			fx.F.Fail("git merge-base --is-ancestor p2 HEAD", shell.Result{Code: 1})
		}, kind: WatchCatchup, kept: true, failed: true},
		"request closed": {task: func(w *WatchedTask) { w.Pr.Status = forge.Closed }, kind: WatchClosed},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := finishtest.NewJanitorFixture(t)
			scriptTarget(fx)
			fx.F.Meta.Script("GH-1/x", branchmeta.Landed, "") // the catchup cleared it
			fx.F.Meta.Script("GH-1/x", branchmeta.Moved, "")
			fx.F.Script("git rev-parse -q --verify HEAD", "h2")
			fx.F.Script("git merge-base --is-ancestor p1 HEAD", "")
			fx.F.Script(plain, "")
			fx.F.Script(lease, "")

			if tc.script != nil {
				tc.script(fx)
			}

			task := undrivenTask()
			if tc.task != nil {
				tc.task(&task)
			}

			root := t.TempDir()

			if tc.held {
				l, err := drivestore.Acquire(root, "GH-1", 2)
				if err != nil {
					t.Fatal(err)
				}

				defer l.Release()
			}

			prompts := 0
			in := WatchInput{
				Main: fx.In.Main, Root: root, Mode: "report", Catchup: true,
				Prompt: func(context.Context, WatchedTask, string) error { prompts++; return nil },
			}

			store := map[string]WatchReport{"GH-1": {
				Kind: WatchCatchup, Key: "catchup sent m2 h1", At: watchNow,
				Catchup: &drivestore.Catchup{Target: "m2", Head: "p1", Origin: "p1", Base: "origin/main", At: watchNow},
			}}

			out := catchupPass(t, in, []WatchedTask{task}, store)

			kind := ""
			if len(out) > 0 {
				kind = out[0].Kind
			}

			if kind != tc.kind {
				t.Errorf("reported %q, want %q: %+v", kind, tc.kind, out)
			}

			for _, push := range []string{plain, lease} {
				if fx.F.Called(push) != (push == tc.push) {
					t.Errorf("%s called: %v", push, fx.F.Called(push))
				}
			}

			rec := store["GH-1"].Catchup
			if (rec != nil) != tc.kept || rec != nil && rec.Failed != tc.failed {
				t.Errorf("record = %+v, want kept %v failed %v", rec, tc.kept, tc.failed)
			}

			if prompts != 0 {
				t.Errorf("a catchup was sent while one was in flight")
			}

			if tc.failed && name != "origin moved meanwhile" {
				// the same fingerprint: nothing more; the target moved: sent
				// again, the mark the failure kept notwithstanding
				if out := catchupPass(t, in, []WatchedTask{task}, store); len(out) != 0 || prompts != 0 {
					t.Errorf("a failed catchup was told again or resent: %+v, %d", out, prompts)
				}

				fx.F.Script("git rev-parse -q --verify refs/remotes/origin/main", "m3")
				fx.F.Fail("git merge-base --is-ancestor m3 main0", shell.Result{Code: 1})
				fx.F.Fail("git merge-base --is-ancestor m3 refs/heads/GH-1/x", shell.Result{Code: 1})
				fx.F.Script("git rev-parse --git-path rebase-merge", "/nonexistent/rebase-merge")
				fx.F.Script("git merge-tree --write-tree --no-messages m3 HEAD", "tree")

				if out := catchupPass(t, in, []WatchedTask{task}, store); len(out) != 1 || prompts != 1 {
					t.Errorf("a moved target did not send a new catchup: %+v, %d", out, prompts)
				}
			}
		})
	}
}

func TestWatchStoreKeepsTheCatchupRecord(t *testing.T) {
	root := t.TempDir()
	rec := &drivestore.Catchup{Target: "m2", Head: "h1", Origin: "p1", Base: "origin/main", At: watchNow, Failed: true}

	want := map[string]WatchReport{"FT-1": {Kind: WatchCatchup, Key: "k", At: watchNow, Catchup: rec}}
	if err := SaveWatchStore(root, want); err != nil {
		t.Fatal(err)
	}

	got, err := LoadWatchStore(root)
	if err != nil {
		t.Fatal(err)
	}

	if r := got["FT-1"].Catchup; r == nil || r.Target != "m2" || r.Head != "h1" || r.Origin != "p1" || !r.Failed ||
		!r.At.Equal(watchNow) {
		t.Errorf("read back %+v", got["FT-1"].Catchup)
	}

	// a newer report keeps the record
	var out collected

	in := WatchInput{Mode: "report", Now: func() time.Time { return watchNow }, Out: &out}
	in.Tell(got, "FT-1", watchNow, &WatchReport{Kind: WatchStopped, Key: "s"})

	if got["FT-1"].Catchup == nil || got["FT-1"].Kind != WatchStopped {
		t.Errorf("after a newer report: %+v", got["FT-1"])
	}
}

// A conflict git stops on is the agent's to settle, named in the prompt, the
// rebase left in progress for it.
func TestWatchHandsAConflictToTheAgent(t *testing.T) {
	fx := finishtest.NewJanitorFixture(t)
	scriptTarget(fx)
	fx.F.Script("git diff --name-only --diff-filter=U -z", "a.go")

	var prompts []string

	in := WatchInput{
		Main: fx.In.Main, Root: t.TempDir(), Mode: "report", Catchup: true, Engine: watchEngine(true),
		Prompt: func(_ context.Context, _ WatchedTask, text string) error {
			prompts = append(prompts, text)

			return nil
		},
	}

	store := map[string]WatchReport{}
	catchupPass(t, in, []WatchedTask{undrivenTask()}, store)

	if len(prompts) != 1 || !strings.Contains(prompts[0], "the watcher rebased GH-1/x onto origin/main") ||
		!strings.Contains(prompts[0], "git stopped on conflicts in a.go") ||
		!strings.Contains(prompts[0], "then run verify and clear the catchup marks") ||
		fx.F.Called("git rebase --abort") || store["GH-1"].Catchup == nil {
		t.Errorf("prompts %q, record %+v", prompts, store["GH-1"].Catchup)
	}
}

// A store written before the catchup record was shared still reads, and its
// record still leases the push against the origin tip it holds.
func TestWatchStoreReadsAnOlderCatchupRecord(t *testing.T) {
	root := t.TempDir()
	old := "FT-1:\n  kind: catchup\n  text: x\n  key: k\n  at: 2026-10-28T12:00:00Z\n" +
		"  catchup:\n    target: m2\n    head: h1\n    origin: p1\n    base: origin/main\n" +
		"    at: 2026-10-28T12:00:00Z\n"

	if err := os.WriteFile(filepath.Join(root, WatchFile), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadWatchStore(root)
	if err != nil {
		t.Fatal(err)
	}

	if r := got["FT-1"].Catchup; r == nil || r.Target != "m2" || r.Origin != "p1" || r.Spent || r.Failed {
		t.Errorf("read back %+v", got["FT-1"].Catchup)
	}
}
