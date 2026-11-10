package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/shell"
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
		task drive.WatchedTask
		mode string
		kind string // "" for no report
		act  bool
	}{
		{name: "owned by a drive", task: drive.WatchedTask{Owner: "drive 3", Pr: mergedPR()}, mode: "finish"},
		{name: "handed back", task: drive.WatchedTask{Off: true, Pr: mergedPR()}, mode: "finish"},
		{
			name: "merged, report mode",
			task: drive.WatchedTask{Pr: mergedPR(), LocalSHA: "abc"},
			mode: "report",
			kind: drive.WatchMerged,
		},
		{
			name: "merged, finish mode",
			task: drive.WatchedTask{Pr: mergedPR(), LocalSHA: "abc", Agent: agentctl.StatusIdle, Listed: true},
			mode: "finish",
			kind: drive.WatchMerged,
			act:  true,
		},
		{
			name: "merged, no workspace", task: drive.WatchedTask{Pr: mergedPR(), LocalSHA: "abc", Listed: true},
			mode: "finish", kind: drive.WatchMerged, act: true,
		},
		{
			name: "merged, agent status unknown", task: drive.WatchedTask{Pr: mergedPR(), LocalSHA: "abc"},
			mode: "finish", kind: drive.WatchMerged,
		},
		{
			name: "merged, agent working",
			task: drive.WatchedTask{Pr: mergedPR(), Agent: agentctl.StatusWorking, Listed: true},
			mode: "finish",
			kind: drive.WatchMerged,
		},
		{
			name: "host did not answer",
			task: drive.WatchedTask{
				HostErr: errors.New("rate limited"),
				Run:     &state.Run{Ship: "done", Updated: watchNow},
			},
			mode: "report",
		},
		{
			name: "merged with commits the PR lacks", task: drive.WatchedTask{Pr: mergedPR(), LocalSHA: "def"},
			mode: "finish", kind: drive.WatchMerged,
		},
		{
			name: "closed", task: drive.WatchedTask{Pr: &forge.PR{URL: "u", Status: forge.Closed}},
			mode: "finish", kind: drive.WatchClosed,
		},
		{name: "open, nothing to say", task: drive.WatchedTask{Pr: &forge.PR{Status: forge.Open}}, mode: "report"},
		{
			name: "stalled",
			task: drive.WatchedTask{Run: running, Agent: agentctl.StatusIdle},
			mode: "report",
			kind: drive.WatchStalled,
		},
		{
			name: "idle, under the grace",
			task: drive.WatchedTask{Run: recent, Agent: agentctl.StatusIdle},
			mode: "report",
		},
		{
			name: "working is never stalled",
			task: drive.WatchedTask{Run: running, Agent: agentctl.StatusWorking},
			mode: "report",
		},
		{name: "blocked", task: drive.WatchedTask{Run: blocked}, mode: "report", kind: drive.WatchStopped},
		{
			name: "shipped",
			task: drive.WatchedTask{Run: &state.Run{Ship: "done", Updated: watchNow}},
			mode: "report",
			kind: drive.WatchStopped,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.task.Id = "FT-1"

			rep, act := drive.DecideWatch(c.task, c.mode, watchNow)

			kind := ""
			if rep != nil {
				kind = rep.Kind
			}

			if kind != c.kind || act != c.act {
				t.Errorf("got %q act=%v, want %q act=%v", kind, act, c.kind, c.act)
			}
		})
	}

	rep, _ := drive.DecideWatch(drive.WatchedTask{Id: "FT-1", Run: blocked}, "report", watchNow)
	if rep.Ask != blocked.Ask || rep.Text != "ship blocked at judge: rethink" {
		t.Errorf("blocked report = %+v", rep)
	}
}

type collected []drive.WatchReport

func (c *collected) Report(r drive.WatchReport) { *c = append(*c, r) }

// The same fact is told once; a new one, and a task gone, change the store.
func TestReconcileReportsOnce(t *testing.T) {
	var out collected

	now := watchNow
	in := drive.WatchInput{Mode: "report", Now: func() time.Time { return now }, Out: &out}
	store := map[string]drive.WatchReport{"FT-9": {Kind: drive.WatchStalled, Key: "x", At: now}}

	blocked := &state.Run{Ship: "blocked", Updated: now}
	tasks := []drive.WatchedTask{{Id: "FT-1", Run: blocked}}

	in.Reconcile(t.Context(), tasks, store)
	in.Reconcile(t.Context(), tasks, store)

	if len(out) != 1 {
		t.Fatalf("reported %d times, want once: %+v", len(out), out)
	}

	if _, ok := store["FT-9"]; ok {
		t.Error("a task gone from the project was kept")
	}

	tasks[0].Run = &state.Run{Ship: "blocked", Updated: now.Add(time.Minute)}
	in.Reconcile(t.Context(), tasks, store)

	if len(out) != 2 {
		t.Errorf("a newer stop was not reported: %+v", out)
	}
}

// A fact coming back after another is not told again: a host that did not
// answer for one pass, or an agent flipping between working and idle.
func TestReconcileNeverRetellsAFact(t *testing.T) {
	var out collected

	in := drive.WatchInput{Mode: "report", Now: func() time.Time { return watchNow }, Out: &out}
	store := map[string]drive.WatchReport{}

	merged := drive.WatchedTask{Id: "FT-1", Pr: mergedPR(), LocalSHA: "abc"}
	ahead := drive.WatchedTask{Id: "FT-1", Pr: mergedPR(), LocalSHA: "def"}

	for _, task := range []drive.WatchedTask{merged, ahead, merged, ahead} {
		in.Reconcile(t.Context(), []drive.WatchedTask{task}, store)
	}

	if len(out) != 2 {
		t.Errorf("told %d reports, want 2: %+v", len(out), out)
	}
}

// Under finish, a merged task is finished and reported as such; a refusal
// is a report naming it, and a lease someone took meanwhile is none.
func TestReconcileFinishes(t *testing.T) {
	task := drive.WatchedTask{Id: "FT-1", Pr: mergedPR(), LocalSHA: "abc", Agent: agentctl.StatusIdle, Listed: true}

	for _, c := range []struct {
		err  error
		kind string
	}{
		{err: nil, kind: drive.WatchFinished},
		{err: errors.New("dirty"), kind: drive.WatchRefused},
		{err: drives.HeldError{Key: "FT-1", Holder: drives.Holder{Drive: 2}}, kind: ""},
	} {
		var out collected

		calls := 0
		in := drive.WatchInput{
			Mode: "finish",
			Now:  func() time.Time { return watchNow },
			Out:  &out,
			Finish: func(context.Context, drive.WatchedTask) error {
				calls++

				return c.err
			},
		}

		in.Reconcile(t.Context(), []drive.WatchedTask{task}, map[string]drive.WatchReport{})

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
	want := map[string]drive.WatchReport{
		"FT-1": {Kind: drive.WatchMerged, Text: "t", Next: "n", Key: "k", At: watchNow},
	}

	if err := drive.SaveWatchStore(root, want); err != nil {
		t.Fatal(err)
	}

	got, err := drive.LoadWatchStore(root)
	if err != nil {
		t.Fatal(err)
	}

	if r := got["FT-1"]; r.Key != "k" || r.Kind != drive.WatchMerged || !r.At.Equal(watchNow) {
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

	fx.f.meta.Seed("GH-2/y", branchmeta.Watch, "false")
	fx.request("GH-1/x", "OPEN", "a1", "me")

	root := t.TempDir()

	lease, err := drives.Acquire(root, "GH-3", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	in := drive.WatchInput{
		Main:     fx.in.Main,
		Root:     root,
		Jan:      fx.in,
		StateDir: t.TempDir(),
		TTL:      time.Hour,
		Now:      time.Now,
	}

	tasks, local, err := drive.CollectWatched(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	if local != 3 {
		t.Errorf("local task branches = %d, want 3: origin's GH-4 does not count", local)
	}

	if len(tasks) != 3 || tasks[0].Id != "GH-1" || tasks[1].Id != "GH-2" || tasks[2].Id != "GH-3" {
		t.Fatalf("tasks = %+v, want GH-1, GH-2 and GH-3, the worktrees", tasks)
	}

	if !strings.HasPrefix(tasks[2].Owner, "drive 2") || fx.f.called(ghHeadOf("GH-3/z")) {
		t.Errorf("GH-3, owned by drive 2: %+v", tasks[2])
	}

	if tasks[0].Pr == nil || tasks[0].Pr.HeadSHA != "a1" || tasks[0].Off {
		t.Errorf("GH-1 = %+v", tasks[0])
	}

	if !tasks[1].Off || fx.f.called(ghHeadOf("GH-2/y")) {
		t.Errorf("GH-2 handed back: off=%v, its requests read: %v", tasks[1].Off, fx.f.called(ghHeadOf("GH-2/y")))
	}
}

// failingAgents lists no workspaces: an agent status nobody can read.
type failingAgents struct{ *memAgents }

func (failingAgents) Workspaces(context.Context) ([]agentctl.Workspace, error) {
	return nil, errors.New("herdr is not running")
}

// Under the lease, the watcher finishes only with the branch still at the
// PR's head and an agent it can see is not working, or no workspace.
func TestRecheckFinish(t *testing.T) {
	task := drive.WatchedTask{Id: "GH-1", Branch: "GH-1/x", Path: "/code/repo.GH-1", Pr: mergedPR()}
	at := func(st agentctl.Status) *memAgents {
		return &memAgents{workspaces: []agentctl.Workspace{{ID: "ws:9", Label: "GH-1", Path: task.Path, Status: st}}}
	}

	cases := []struct {
		name    string
		tip     string
		backend agentctl.Backend
		ok      bool
	}{
		{name: "idle at the head", tip: "abc", backend: at(agentctl.StatusIdle), ok: true},
		{name: "no workspace", tip: "abc", backend: &memAgents{}, ok: true},
		{name: "working", tip: "abc", backend: at(agentctl.StatusWorking)},
		{name: "listing failed", tip: "abc", backend: failingAgents{&memAgents{}}},
		{name: "no backend", tip: "abc"},
		{name: "a commit the PR lacks", tip: "def", backend: at(agentctl.StatusIdle)},
		{name: "the tip unreadable", tip: "fail", backend: at(agentctl.StatusIdle)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakes(t)
			if c.tip == "fail" {
				f.fail(
					"git rev-parse -q --verify refs/heads/GH-1/x",
					shell.Result{Code: 128, Stderr: "fatal: bad object"},
				)
			} else {
				f.script("git rev-parse -q --verify refs/heads/GH-1/x", c.tip)
			}

			err := drive.RecheckFinish(t.Context(), c.backend, t.TempDir(), task)
			if (err == nil) != c.ok {
				t.Errorf("err = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

// A host that does not answer is said every pass, names the tasks it left
// out, and leaves the store once the host answers again.
func TestHostTrouble(t *testing.T) {
	var out collected

	in := drive.WatchInput{Mode: "report", Now: func() time.Time { return watchNow }, Out: &out}
	store := map[string]drive.WatchReport{}
	tasks := []drive.WatchedTask{{Id: "FT-1", HostErr: errors.New("gh: HTTP 401")}, {Id: "FT-2"}}

	in.HostTrouble(tasks, store)
	in.HostTrouble(tasks, store)

	if len(out) != 2 || !strings.Contains(out[0].Text, "HTTP 401") || !strings.Contains(out[0].Text, "FT-1 left out") {
		t.Errorf("reports = %+v", out)
	}

	if _, ok := store[drive.WatchHostKey]; !ok {
		t.Error("f10 watch would not show the host trouble")
	}

	in.HostTrouble(tasks[1:], store)

	if _, ok := store[drive.WatchHostKey]; ok {
		t.Error("the host answered, and its trouble stayed")
	}
}

// A handed-back mark git cannot read counts as handed back for the pass:
// the task is skipped, its request never read, and a line names it.
func TestCollectWatchedSkipsAnUnreadableMark(t *testing.T) {
	fx := newJanitorFixture(t)
	fx.branches(t, [3]string{"GH-1/x", "a1", "a1"})
	fx.f.script(worktrees, porcelain("GH-1/x", "/code/repo.GH-1"))
	fx.f.meta.FailRead("GH-1/x", branchmeta.Watch, errors.New("fatal: unable to read config"))

	var warned []string

	in := drive.WatchInput{
		Main: fx.in.Main, Root: t.TempDir(), Jan: fx.in, StateDir: t.TempDir(), TTL: time.Hour, Now: time.Now,
		Warn: func(line string) { warned = append(warned, line) },
	}

	tasks, _, err := drive.CollectWatched(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	if len(tasks) != 1 || !tasks[0].Off || fx.f.called(ghHeadOf("GH-1/x")) {
		t.Errorf("tasks = %+v, request read: %v", tasks, fx.f.called(ghHeadOf("GH-1/x")))
	}

	if len(warned) != 1 || !strings.Contains(warned[0], "GH-1/x") ||
		!strings.Contains(warned[0], "unable to read config") {
		t.Errorf("warned = %q", warned)
	}

	if rep, _ := drive.DecideWatch(tasks[0], "finish", time.Now()); rep != nil {
		t.Errorf("a task whose mark is unreadable got a report: %+v", rep)
	}
}

// Under watch: finish a deferred pull is retried each pass, told once, and
// keeps the project watched until it lands.
func TestWatchRetriesADeferredPull(t *testing.T) {
	f := newFakes(t)
	main, root := t.TempDir(), t.TempDir()

	if err := os.WriteFile(filepath.Join(root, finish.PendingPullFile), []byte("main m3rge\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	f.script(curBr, "main")
	f.fail("git pull --ff-only", shell.Result{Code: 1, Stderr: "fatal: Not possible to fast-forward, aborting."})

	var (
		out  collected
		said []string
	)

	in := drive.WatchInput{
		Mode: "finish", Main: main, Root: root, Out: &out,
		Now:  func() time.Time { return watchNow },
		Warn: func(l string) { said = append(said, l) },
	}
	store := map[string]drive.WatchReport{}

	for range 2 {
		if !in.RetryPull(t.Context(), store) {
			t.Fatal("a deferred pull left the project unwatched")
		}
	}

	if len(out) != 1 || out[0].Kind != drive.WatchUnpulled {
		t.Errorf("reports = %+v, want one unpulled", out)
	}

	f.script("git pull --ff-only", "")
	f.script("git merge-base --is-ancestor m3rge main", "")

	if in.RetryPull(t.Context(), store) {
		t.Error("a landed pull still keeps the project")
	}

	if _, ok := store[drive.WatchPullKey]; ok || len(said) != 1 {
		t.Errorf("store %v, said %q; want the entry gone and the pull said", store, said)
	}

	if (drive.WatchInput{Mode: "report", Root: root, Main: main}).RetryPull(t.Context(), store) {
		t.Error("watch: report pulled")
	}
}
