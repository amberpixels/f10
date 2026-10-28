package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/shell"
)

// undrivenTask is an undriven task with an open PR on main, its agent idle.
func undrivenTask() watchedTask {
	return watchedTask{
		id: "GH-1", branch: "GH-1/x", path: "/code/repo.GH-1", localSHA: "p1",
		pr:    &forge.PR{URL: "https://gh/pr/9", Status: forge.Open, Base: "main"},
		agent: agentctl.StatusIdle, listed: true, ws: "ws-1",
	}
}

// scriptTarget scripts what a pass reads after its fetch for GH-1/x on
// origin at p1, with origin/main at m2 past the local main at main0.
func scriptTarget(fx *janitorFixture) {
	f := fx.f
	f.script("git fetch origin", "")
	f.script("git rev-parse -q --verify refs/remotes/origin/GH-1/x", "p1")
	f.script("git rev-parse -q --verify refs/heads/main", "main0")
	f.script("git rev-parse -q --verify refs/remotes/origin/main", "m2")
	f.fail("git merge-base --is-ancestor m2 main0", shell.Result{Code: 1})
	f.script("git rev-parse -q --verify MERGE_HEAD", "")
	f.script("git rev-parse -q --verify REBASE_HEAD", "")
	f.script("git status --porcelain", "")
	f.fail("git merge-base --is-ancestor m2 refs/heads/GH-1/x", shell.Result{Code: 1})
	f.script("git rev-parse -q --verify HEAD", "p1")
	f.script("git config branch.GH-1/x."+cfgLanded+" main", "")
}

// A pass under watch_catchup: on, as watchPass runs it after collecting.
func catchupPass(t *testing.T, in watchInput, tasks []watchedTask, store map[string]watchReport) collected {
	t.Helper()

	var out collected

	in.out = &out
	if in.now == nil {
		in.now = func() time.Time { return watchNow }
	}

	if in.catchup {
		in.readTargets(t.Context(), tasks, store)
	}

	in.reconcile(t.Context(), tasks, store)

	return out
}

func TestWatchCatchesUpAnOpenPR(t *testing.T) {
	const mark = "git config branch.GH-1/x." + cfgLanded + " main"

	cases := map[string]struct {
		key    bool
		task   func(*watchedTask)
		script func(*janitorFixture)
		lease  bool // a drive holds the task
		prompt bool // a catchup went out
		kind   string
		warned string
	}{
		"key off":             {task: func(*watchedTask) {}},
		"behind, idle, clean": {key: true, prompt: true, kind: watchCatchup},
		"agent working":       {key: true, task: func(w *watchedTask) { w.agent = agentctl.StatusWorking }},
		"no listing":          {key: true, task: func(w *watchedTask) { w.listed = false; w.agent = "" }},
		"tree dirty": {key: true, script: func(fx *janitorFixture) {
			fx.f.script("git status --porcelain", " M a.go")
		}},
		"merge in progress": {key: true, script: func(fx *janitorFixture) {
			fx.f.script("git rev-parse -q --verify MERGE_HEAD", "mh")
		}},
		"already an ancestor": {key: true, script: func(fx *janitorFixture) {
			fx.f.script("git merge-base --is-ancestor m2 refs/heads/GH-1/x", "")
		}},
		// a mark a finish or a ship guard left is a catchup nobody else sends
		"already marked": {key: true, script: func(fx *janitorFixture) {
			fx.f.script("git config --get branch.GH-1/x."+cfgLanded, "main")
		}, prompt: true, kind: watchCatchup},
		"diverged from origin": {key: true, task: func(w *watchedTask) { w.localSHA = "h9" }, kind: watchBehind},
		"not on origin": {key: true, script: func(fx *janitorFixture) {
			fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-1/x", "")
		}},
		"no workspace": {key: true, task: func(w *watchedTask) { w.ws, w.agent = "", "" }, kind: watchBehind},
		"retargeted":   {key: true, task: func(w *watchedTask) { w.pr.Base = "feature" }, kind: watchBehind},
		"closed":       {key: true, task: func(w *watchedTask) { w.pr.Status = forge.Closed }, kind: watchClosed},
		"fetch fails": {key: true, script: func(fx *janitorFixture) {
			fx.f.fail("git fetch origin", shell.Result{Code: 128, Stderr: "no route to host"})
		}, warned: "no route to host"},
		"lease held":  {key: true, lease: true},
		"handed back": {key: true, task: func(w *watchedTask) { w.off = true }},
		"owned":       {key: true, task: func(w *watchedTask) { w.owner = "drive 2" }},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newJanitorFixture(t)
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
				lease, err := drives.Acquire(root, "GH-1", 2)
				if err != nil {
					t.Fatal(err)
				}

				defer lease.Release()
			}

			var (
				prompts []string
				warned  []string
			)

			in := watchInput{
				main: fx.in.main, root: root, mode: "report", catchup: tc.key,
				prompt: func(_ context.Context, _ watchedTask, text string) error {
					prompts = append(prompts, text)

					return nil
				},
				warn: func(line string) { warned = append(warned, line) },
			}

			store := map[string]watchReport{}
			out := catchupPass(t, in, []watchedTask{task}, store)

			kind := ""
			if len(out) > 0 {
				kind = out[0].Kind
			}

			if kind != tc.kind {
				t.Errorf("reported %q, want %q: %+v", kind, tc.kind, out)
			}

			if (len(prompts) > 0) != tc.prompt || fx.f.called(mark) != tc.prompt {
				t.Errorf("prompted %v (marked %v), want %v", prompts, fx.f.called(mark), tc.prompt)
			}

			if tc.prompt {
				if !strings.Contains(prompts[0], "/f10:catchup --driven") ||
					!strings.Contains(prompts[0], watchClause) {
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

			if !tc.key || tc.task != nil && (task.off || task.owner != "" || !task.listed) {
				if fx.f.called("git fetch origin") {
					t.Error("origin was fetched")
				}
			}

			if tc.warned != "" && (len(warned) != 1 || !strings.Contains(warned[0], tc.warned)) {
				t.Errorf("warned = %q, want %q", warned, tc.warned)
			}

			// the same pass again tells nothing new and sends nothing twice;
			// a sent catchup has its agent working on it
			if tc.prompt {
				task.agent = agentctl.StatusWorking
			}

			out = catchupPass(t, in, []watchedTask{task}, store)
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
		fx := newJanitorFixture(t)
		scriptTarget(fx)
		fx.f.script("git config --get branch.GH-1/x."+cfgAfterBranch, "GH-9/y")
		fx.f.script("git rev-parse -q --verify refs/heads/GH-9/y", "b2")
		fx.f.fail("git merge-base --is-ancestor b2 refs/heads/GH-1/x", shell.Result{Code: 1})
		fx.f.script("git config branch.GH-1/x."+cfgMoved+" b2", "")

		if onOrigin {
			fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-9/y", "b2")
			fx.f.script("git merge-base --is-ancestor b2 refs/remotes/origin/GH-9/y", "")
		} else {
			fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-9/y", "")
			fx.f.fail("git merge-base --is-ancestor b2 refs/remotes/origin/GH-9/y", shell.Result{Code: 128})
		}

		task := undrivenTask()
		task.pr.Base = "GH-9/y"

		prompts := 0
		in := watchInput{
			main: fx.in.main, root: t.TempDir(), mode: "report", catchup: true,
			prompt: func(context.Context, watchedTask, string) error { prompts++; return nil },
		}

		store := map[string]watchReport{}
		out := catchupPass(t, in, []watchedTask{task}, store)
		marked := fx.f.called("git config branch.GH-1/x." + cfgMoved + " b2")

		want := map[bool]int{true: 1, false: 0}[onOrigin]
		if prompts != want || marked != onOrigin || len(out) != want {
			t.Errorf("on origin %v: prompted %d, marked %v, reported %+v", onOrigin, prompts, marked, out)
		}

		if rec := store["GH-1"].Catchup; onOrigin && (rec == nil || rec.Target != "b2" || rec.Base != "GH-9/y") {
			t.Errorf("record = %+v", rec)
		}
	}
}

// An agent the backend cannot find is told once, the mark taken back.
func TestWatchReportsAnAgentItCannotPrompt(t *testing.T) {
	fx := newJanitorFixture(t)
	scriptTarget(fx)
	fx.f.script("git config --unset branch.GH-1/x."+cfgLanded, "")

	in := watchInput{
		main: fx.in.main, root: t.TempDir(), mode: "report", catchup: true,
		prompt: func(context.Context, watchedTask, string) error { return agentctl.ErrAgentNotFound },
	}

	store := map[string]watchReport{}
	out := catchupPass(t, in, []watchedTask{undrivenTask()}, store)

	if len(out) != 1 || out[0].Kind != watchBehind || !strings.Contains(out[0].Text, "not found") ||
		!fx.f.called("git config --unset branch.GH-1/x."+cfgLanded) || store["GH-1"].Catchup != nil {
		t.Errorf("reported %+v, unset %v, record %+v", out, fx.f.called("git config --unset"), store["GH-1"].Catchup)
	}
}

func TestWatchSettlesASentCatchup(t *testing.T) {
	const (
		plain = "git push origin HEAD:refs/heads/GH-1/x"
		lease = "git push --force-with-lease=refs/heads/GH-1/x:p1 origin HEAD:refs/heads/GH-1/x"
	)

	cases := map[string]struct {
		script func(*janitorFixture)
		task   func(*watchedTask)
		held   bool
		push   string // the push expected, "" for none
		kind   string
		kept   bool // the record stays
		failed bool // and is marked failed
	}{
		"ended clean, merged": {push: plain, kind: watchCatchup},
		"ended clean, rebased": {script: func(fx *janitorFixture) {
			fx.f.fail("git merge-base --is-ancestor p1 HEAD", shell.Result{Code: 1})
		}, push: lease, kind: watchCatchup},
		"head equals origin": {script: func(fx *janitorFixture) {
			fx.f.script("git rev-parse -q --verify HEAD", "p1")
		}},
		"agent still working": {task: func(w *watchedTask) { w.agent = agentctl.StatusWorking }, kept: true},
		"mark still set": {script: func(fx *janitorFixture) {
			fx.f.script("git config --get branch.GH-1/x."+cfgLanded, "main")
		}, kind: watchCatchup, kept: true, failed: true},
		"rebase left open": {script: func(fx *janitorFixture) {
			fx.f.script("git rev-parse -q --verify REBASE_HEAD", "rh")
		}, kind: watchCatchup, kept: true, failed: true},
		"lease held": {held: true},
		"push rejected": {script: func(fx *janitorFixture) {
			fx.f.fail(plain, shell.Result{Code: 1, Stderr: "rejected"})
		}, push: plain, kind: watchCatchup, kept: true},
		// a commit pushed to the request meanwhile is never overwritten
		"origin moved meanwhile": {script: func(fx *janitorFixture) {
			fx.f.script("git rev-parse -q --verify refs/remotes/origin/GH-1/x", "p2")
		}, kind: watchCatchup, kept: true, failed: true},
		"request closed": {task: func(w *watchedTask) { w.pr.Status = forge.Closed }, kind: watchClosed},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newJanitorFixture(t)
			scriptTarget(fx)
			fx.f.script("git config --get branch.GH-1/x."+cfgLanded, "") // the catchup cleared it
			fx.f.script("git config --get branch.GH-1/x."+cfgMoved, "")
			fx.f.script("git rev-parse -q --verify HEAD", "h2")
			fx.f.script("git merge-base --is-ancestor p1 HEAD", "")
			fx.f.script(plain, "")
			fx.f.script(lease, "")

			if tc.script != nil {
				tc.script(fx)
			}

			task := undrivenTask()
			if tc.task != nil {
				tc.task(&task)
			}

			root := t.TempDir()

			if tc.held {
				l, err := drives.Acquire(root, "GH-1", 2)
				if err != nil {
					t.Fatal(err)
				}

				defer l.Release()
			}

			prompts := 0
			in := watchInput{
				main: fx.in.main, root: root, mode: "report", catchup: true,
				prompt: func(context.Context, watchedTask, string) error { prompts++; return nil },
			}

			store := map[string]watchReport{"GH-1": {
				Kind: watchCatchup, Key: "catchup sent m2 h1", At: watchNow,
				Catchup: &sentCatchup{Target: "m2", Head: "p1", Origin: "p1", Base: "origin/main", At: watchNow},
			}}

			out := catchupPass(t, in, []watchedTask{task}, store)

			kind := ""
			if len(out) > 0 {
				kind = out[0].Kind
			}

			if kind != tc.kind {
				t.Errorf("reported %q, want %q: %+v", kind, tc.kind, out)
			}

			for _, push := range []string{plain, lease} {
				if fx.f.called(push) != (push == tc.push) {
					t.Errorf("%s called: %v", push, fx.f.called(push))
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
				// the same target and head: nothing more; the target moved: sent
				// again, the mark the failure kept notwithstanding
				if out := catchupPass(t, in, []watchedTask{task}, store); len(out) != 0 || prompts != 0 {
					t.Errorf("a failed catchup was told again or resent: %+v, %d", out, prompts)
				}

				fx.f.script("git rev-parse -q --verify refs/remotes/origin/main", "m3")
				fx.f.fail("git merge-base --is-ancestor m3 main0", shell.Result{Code: 1})
				fx.f.fail("git merge-base --is-ancestor m3 refs/heads/GH-1/x", shell.Result{Code: 1})
				fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")

				if out := catchupPass(t, in, []watchedTask{task}, store); len(out) != 1 || prompts != 1 {
					t.Errorf("a moved target did not send a new catchup: %+v, %d", out, prompts)
				}
			}
		})
	}
}

func TestWatchStoreKeepsTheCatchupRecord(t *testing.T) {
	root := t.TempDir()
	rec := &sentCatchup{Target: "m2", Head: "h1", Origin: "p1", Base: "origin/main", At: watchNow, Failed: true}

	want := map[string]watchReport{"FT-1": {Kind: watchCatchup, Key: "k", At: watchNow, Catchup: rec}}
	if err := saveWatchStore(root, want); err != nil {
		t.Fatal(err)
	}

	got, err := loadWatchStore(root)
	if err != nil {
		t.Fatal(err)
	}

	if r := got["FT-1"].Catchup; r == nil || r.Target != "m2" || r.Head != "h1" || r.Origin != "p1" || !r.Failed ||
		!r.At.Equal(watchNow) {
		t.Errorf("read back %+v", got["FT-1"].Catchup)
	}

	// a newer report keeps the record
	var out collected

	in := watchInput{mode: "report", now: func() time.Time { return watchNow }, out: &out}
	in.tell(got, "FT-1", watchNow, &watchReport{Kind: watchStopped, Key: "s"})

	if got["FT-1"].Catchup == nil || got["FT-1"].Kind != watchStopped {
		t.Errorf("after a newer report: %+v", got["FT-1"])
	}
}
