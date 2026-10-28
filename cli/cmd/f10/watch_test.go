package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/state"
)

var watchNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func mergedPR() *forge.PR {
	return &forge.PR{URL: "https://gh/pr/9", Status: forge.Merged, HeadSHA: "abc"}
}

func TestDecideWatch(t *testing.T) {
	running := &state.Run{Ship: "running", Leaf: "implement", Updated: watchNow.Add(-20 * time.Minute)}
	recent := &state.Run{Ship: "running", Leaf: "implement", Updated: watchNow.Add(-time.Minute)}
	blocked := &state.Run{
		Ship: "blocked", Leaf: "judge", Note: "rethink", Ask: "1. Go on? [yes | no]", Updated: watchNow,
	}

	cases := []struct {
		name string
		task watchedTask
		mode string
		kind string // "" for no report
		act  bool
	}{
		{name: "owned by a drive", task: watchedTask{owner: "drive 3", pr: mergedPR()}, mode: "finish"},
		{name: "handed back", task: watchedTask{off: true, pr: mergedPR()}, mode: "finish"},
		{
			name: "merged, report mode",
			task: watchedTask{pr: mergedPR(), localSHA: "abc"},
			mode: "report",
			kind: watchMerged,
		},
		{
			name: "merged, finish mode",
			task: watchedTask{pr: mergedPR(), localSHA: "abc", agent: agentctl.StatusIdle, listed: true},
			mode: "finish",
			kind: watchMerged,
			act:  true,
		},
		{
			name: "merged, no workspace", task: watchedTask{pr: mergedPR(), localSHA: "abc", listed: true},
			mode: "finish", kind: watchMerged, act: true,
		},
		{
			name: "merged, agent status unknown", task: watchedTask{pr: mergedPR(), localSHA: "abc"},
			mode: "finish", kind: watchMerged,
		},
		{
			name: "merged, agent working",
			task: watchedTask{pr: mergedPR(), agent: agentctl.StatusWorking, listed: true},
			mode: "finish",
			kind: watchMerged,
		},
		{
			name: "host did not answer",
			task: watchedTask{hostless: true, run: &state.Run{Ship: "done", Updated: watchNow}},
			mode: "report",
		},
		{
			name: "merged with commits the PR lacks", task: watchedTask{pr: mergedPR(), localSHA: "def"},
			mode: "finish", kind: watchMerged,
		},
		{
			name: "closed", task: watchedTask{pr: &forge.PR{URL: "u", Status: forge.Closed}},
			mode: "finish", kind: watchClosed,
		},
		{name: "open, nothing to say", task: watchedTask{pr: &forge.PR{Status: forge.Open}}, mode: "report"},
		{
			name: "stalled",
			task: watchedTask{run: running, agent: agentctl.StatusIdle},
			mode: "report",
			kind: watchStalled,
		},
		{name: "idle, under the grace", task: watchedTask{run: recent, agent: agentctl.StatusIdle}, mode: "report"},
		{
			name: "working is never stalled",
			task: watchedTask{run: running, agent: agentctl.StatusWorking},
			mode: "report",
		},
		{name: "blocked", task: watchedTask{run: blocked}, mode: "report", kind: watchStopped},
		{
			name: "shipped",
			task: watchedTask{run: &state.Run{Ship: "done", Updated: watchNow}},
			mode: "report",
			kind: watchStopped,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.task.id = "FT-1"

			rep, act := decideWatch(c.task, c.mode, watchNow)

			kind := ""
			if rep != nil {
				kind = rep.Kind
			}

			if kind != c.kind || act != c.act {
				t.Errorf("got %q act=%v, want %q act=%v", kind, act, c.kind, c.act)
			}
		})
	}

	rep, _ := decideWatch(watchedTask{id: "FT-1", run: blocked}, "report", watchNow)
	if rep.Ask != blocked.Ask || rep.Text != "ship blocked at judge: rethink" {
		t.Errorf("blocked report = %+v", rep)
	}
}

type collected []watchReport

func (c *collected) report(r watchReport) { *c = append(*c, r) }

// The same fact is told once; a new one, and a task gone, change the store.
func TestReconcileReportsOnce(t *testing.T) {
	var out collected

	now := watchNow
	in := watchInput{mode: "report", now: func() time.Time { return now }, out: &out}
	store := map[string]watchReport{"FT-9": {Kind: watchStalled, Key: "x", At: now}}

	blocked := &state.Run{Ship: "blocked", Updated: now}
	tasks := []watchedTask{{id: "FT-1", run: blocked}}

	in.reconcile(t.Context(), tasks, store)
	in.reconcile(t.Context(), tasks, store)

	if len(out) != 1 {
		t.Fatalf("reported %d times, want once: %+v", len(out), out)
	}

	if _, ok := store["FT-9"]; ok {
		t.Error("a task gone from the project was kept")
	}

	tasks[0].run = &state.Run{Ship: "blocked", Updated: now.Add(time.Minute)}
	in.reconcile(t.Context(), tasks, store)

	if len(out) != 2 {
		t.Errorf("a newer stop was not reported: %+v", out)
	}
}

// A fact coming back after another is not told again: a host that did not
// answer for one pass, or an agent flipping between working and idle.
func TestReconcileNeverRetellsAFact(t *testing.T) {
	var out collected

	in := watchInput{mode: "report", now: func() time.Time { return watchNow }, out: &out}
	store := map[string]watchReport{}

	merged := watchedTask{id: "FT-1", pr: mergedPR(), localSHA: "abc"}
	ahead := watchedTask{id: "FT-1", pr: mergedPR(), localSHA: "def"}

	for _, task := range []watchedTask{merged, ahead, merged, ahead} {
		in.reconcile(t.Context(), []watchedTask{task}, store)
	}

	if len(out) != 2 {
		t.Errorf("told %d reports, want 2: %+v", len(out), out)
	}
}

// Under finish, a merged task is finished and reported as such; a refusal
// is a report naming it, and a lease someone took meanwhile is none.
func TestReconcileFinishes(t *testing.T) {
	task := watchedTask{id: "FT-1", pr: mergedPR(), localSHA: "abc", agent: agentctl.StatusIdle, listed: true}

	for _, c := range []struct {
		err  error
		kind string
	}{
		{err: nil, kind: watchFinished},
		{err: errors.New("dirty"), kind: watchRefused},
		{err: drives.HeldError{Key: "FT-1", Holder: drives.Holder{Drive: 2}}, kind: ""},
	} {
		var out collected

		calls := 0
		in := watchInput{
			mode: "finish",
			now:  func() time.Time { return watchNow },
			out:  &out,
			finish: func(context.Context, watchedTask) error {
				calls++

				return c.err
			},
		}

		in.reconcile(t.Context(), []watchedTask{task}, map[string]watchReport{})

		if calls != 1 {
			t.Errorf("%v: finish ran %d times", c.err, calls)
		}

		kind := ""
		if len(out) > 0 {
			kind = out[0].Kind
		}

		if kind != c.kind {
			t.Errorf("%v: reported %q, want %q", c.err, kind, c.kind)
		}
	}
}

func TestWatchStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := map[string]watchReport{"FT-1": {Kind: watchMerged, Text: "t", Next: "n", Key: "k", At: watchNow}}

	if err := saveWatchStore(root, want); err != nil {
		t.Fatal(err)
	}

	got, err := loadWatchStore(root)
	if err != nil {
		t.Fatal(err)
	}

	if r := got["FT-1"]; r.Key != "k" || r.Kind != watchMerged || !r.At.Equal(watchNow) {
		t.Errorf("read back %+v", got)
	}
}

// collectWatched reads only task worktrees, marks an owned and a handed
// back one, and counts only local task branches toward watching.
func TestCollectWatched(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t,
		[3]string{"GH-1/x", "a1", "a1"},
		[3]string{"GH-2/y", "b2", ""},
		[3]string{"GH-3/z", "c3", ""},
		[3]string{"GH-4/w", "", "d4"},
	)
	fx.f.script(
		worktrees,
		porcelain("GH-1/x", "/code/repo.GH-1", "GH-2/y", "/code/repo.GH-2", "GH-3/z", "/code/repo.GH-3"),
	)
	fx.f.script("git config --get branch.GH-1/x."+cfgWatch, "")
	fx.f.script("git config --get branch.GH-2/y."+cfgWatch, "false")
	fx.request("GH-1/x", "OPEN", "a1", "me")

	root := t.TempDir()

	lease, err := drives.Acquire(root, "GH-3", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	in := watchInput{main: fx.in.main, root: root, jan: fx.in, stateDir: t.TempDir(), ttl: time.Hour, now: time.Now}

	tasks, local, err := collectWatched(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	if local != 3 {
		t.Errorf("local task branches = %d, want 3: origin's GH-4 does not count", local)
	}

	if len(tasks) != 3 || tasks[0].id != "GH-1" || tasks[1].id != "GH-2" || tasks[2].id != "GH-3" {
		t.Fatalf("tasks = %+v, want GH-1, GH-2 and GH-3, the worktrees", tasks)
	}

	if !strings.HasPrefix(tasks[2].owner, "drive 2") || fx.f.called(ghHeadOf("GH-3/z")) {
		t.Errorf("GH-3, owned by drive 2: %+v", tasks[2])
	}

	if tasks[0].pr == nil || tasks[0].pr.HeadSHA != "a1" || tasks[0].off {
		t.Errorf("GH-1 = %+v", tasks[0])
	}

	if !tasks[1].off || fx.f.called(ghHeadOf("GH-2/y")) {
		t.Errorf("GH-2 handed back: off=%v, its requests read: %v", tasks[1].off, fx.f.called(ghHeadOf("GH-2/y")))
	}
}
