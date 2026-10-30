package main

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
)

// A task another drive holds is skipped, naming that drive: nothing is
// opened or prompted for it.
func TestDriveSkipsATaskAnotherDriveOwns(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.in.root = t.TempDir()
	fx.task("GH-1", taskInfo{body: "x"}, "")

	// bouncing off the lease says nothing about the owner's task
	fx.in.record = func(_ context.Context, _ string, e ledger.Event) { t.Errorf("recorded %+v", e) }

	other, err := drives.Acquire(fx.in.root, "GH-1", 7)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()

	err = fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, exitHalted, fx.out.String())
	}

	want := "GH-1:    owned by drive 7 (pid " + strconv.Itoa(os.Getpid()) + ")"
	if !strings.Contains(fx.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, fx.out.String())
	}

	if fx.f.called("wt switch") || fx.f.called("herdr agent prompt") {
		t.Errorf("an owned task was acted on: %v", fx.f.calls)
	}
}

// A drive that asks leaves its record with the outcome, and is answered
// by its number; rerunning the same command picks the record up, and the
// answer it takes is recorded.
func TestDriveRecordIsPickedUpByTheSameCommand(t *testing.T) {
	root := t.TempDir()

	fx := newDriveFixture(t, "GH-1", "judge")
	fx.in.root = root
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, shipFlow{}, false), "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]") }

	if err := fx.drive(t); exitCode(err) != exitAsk {
		t.Fatalf("err = %v", err)
	}

	if want := `answer with: f10 drive --resume 1 --answer "1. … 2. …"`; !strings.Contains(fx.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, fx.out.String())
	}

	rec, err := drives.Read(root, 1)
	if err != nil {
		t.Fatal(err)
	}

	if o := rec.Task("GH-1").Outcome; o == nil || o.Row != "asked at judge" || o.Code != exitAsk {
		t.Errorf("recorded outcome = %+v", o)
	}

	// the drive's leases went with it
	if _, held := drives.Probe(root, "GH-1"); held {
		t.Error("the task's lease outlived the drive")
	}

	fx = newDriveFixture(t, "GH-1", "judge", "--answer", "1. proceed")
	fx.in.root = root
	fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
	fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1",
		`{"result":{"workspace":{"workspace_id":"ws:GH-1"},"root_pane":{"pane_id":"pane:1"},"already_open":true}}`)
	fx.f.script("herdr agent get pane:1", agentFound)
	fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" 1. proceed", "{}")
	fx.f.script("git config branch.GH-1."+cfgDrive+" judge", "")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[1] = func() {
		// the task runs again: the outcome its last run left is gone
		if rec, err := drives.Read(root, 1); err != nil || rec.Task("GH-1").Outcome != nil {
			t.Errorf("record while running = %+v, %v", rec.Task("GH-1"), err)
		}
	}
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if !strings.HasPrefix(fx.out.String(), "drive 1\n") {
		t.Errorf("the rerun did not pick up drive 1:\n%s", fx.out.String())
	}

	rec, err = drives.Read(root, 1)
	if err != nil {
		t.Fatal(err)
	}

	if o := rec.Task("GH-1").Outcome; o == nil || o.Row != "done: judge" || !rec.Done() {
		t.Errorf("recorded outcome = %+v", o)
	}

	if len(rec.Answered) != 1 || rec.Answered[0].Text != "1. proceed" || len(rec.Answers) != 0 {
		t.Errorf("answers = %+v, answered = %+v", rec.Answers, rec.Answered)
	}

	if all, _ := drives.List(root); len(all) != 1 {
		t.Errorf("%d records, want the one picked up", len(all))
	}
}

// An answer the drive reports unused is dropped from the record, so a
// resume never sends it to a later question.
func TestDriveDropsUnusedAnswers(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge", "--answer", "GH-1=1. proceed")
	fx.in.root = t.TempDir()
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "--answer for GH-1 went unused") {
		t.Errorf("output:\n%s", fx.out.String())
	}

	rec, err := drives.Read(fx.in.root, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(rec.Answers) != 0 {
		t.Errorf("record keeps %v", rec.Answers)
	}
}

// An answer a drive kept, because it stopped before an agent took it, is
// given again on the rerun, unless the rerun gives that task another.
func TestOpenRecordKeepsAnswersNotTaken(t *testing.T) {
	root := t.TempDir()

	rec := &drives.Record{Args: []string{"GH-1", "GH-2"}, Answers: map[string]string{"GH-1": "1. yes", "GH-2": "1. no"}}
	if err := drives.Create(root, rec); err != nil {
		t.Fatal(err)
	}

	in := &driveInput{root: root, argv: []string{"GH-1", "GH-2", "--answer", "GH-2=2. maybe"}, now: time.Now}
	in.args.answers = []string{"GH-2=2. maybe"}

	if err := in.openRecord(nil); err != nil {
		t.Fatal(err)
	}
	defer in.releaseAll()

	if want := []string{"GH-1=1. yes", "GH-2=2. maybe"}; !slices.Equal(in.args.answers, want) {
		t.Errorf("answers = %q, want %q", in.args.answers, want)
	}

	// the drive's own lease is held: a second drive on the record refuses
	second := &driveInput{root: root, argv: []string{"GH-1", "GH-2"}, now: time.Now}
	if err := second.openRecord(nil); err == nil || !strings.Contains(err.Error(), "drive 1 is running") {
		t.Errorf("second openRecord = %v", err)
	}
}

// The agent got past a skill before the drive could send it, as after a
// drive stopped mid-ship: the skill is counted done, not prompted again.
func TestDriveSkipsAPromptTheAgentAlreadyFinished(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "plan", "ship")
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "plan")
	fx.f.script("git config branch.GH-1."+cfgDrive+" ship", "")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"),
		listing("GH-1", "idle"))
	// plan's turn ends with ship done too
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if fx.f.called("herdr agent prompt " + agentOf("GH-1") + " /f10:ship") {
		t.Error("ship was prompted again")
	}

	for _, want := range []string{"GH-1 ship: already done", "GH-1:    done: plan ship"} {
		if !strings.Contains(fx.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, fx.out.String())
		}
	}
}

// A drive cancelled mid-run sends nothing more: its tasks end cancelled
// and their worktrees stay.
func TestDriveStopsWhenCancelled(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge", "ship")
	fx.in.root = t.TempDir()
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"))
	fx.in.record = func(_ context.Context, _ string, e ledger.Event) { t.Errorf("recorded %+v", e) }
	fx.onTick[1] = func() {
		var out strings.Builder
		if err := driveCancel(&out, fx.in.root, 1, fx.clock); err != nil {
			t.Error(err)
		}

		if !strings.Contains(out.String(), "drive 1 cancelled: pid") {
			t.Errorf("cancel said %q", out.String())
		}
	}

	if err := fx.drive(t); exitCode(err) != exitHalted {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "GH-1:    cancelled") {
		t.Errorf("output:\n%s", fx.out.String())
	}

	if fx.f.called("herdr agent prompt "+agentOf("GH-1")+" /f10:ship") || fx.f.called("git worktree remove") {
		t.Errorf("acted after the cancel: %v", fx.f.calls)
	}

	var list strings.Builder
	if err := driveList(&list, fx.in.root, false, nil); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"drive 1 ", "  cancelled\n", "f10 drive GH-1 judge ship", "└─ GH-1:  cancelled"} {
		if !strings.Contains(list.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, list.String())
		}
	}
}

// A stopped drive's open rows whose task finished outside it read finished,
// and the record on disk says so; a row whose task is not finished stays.
func TestDriveListHealsRowsFinishedOutside(t *testing.T) {
	root := t.TempDir()
	rec := &drives.Record{Args: []string{"GH-1", "GH-2", "GH-3"}, Tasks: []drives.Task{
		{ID: "GH-1", Outcome: &drives.Outcome{Row: "halted at catchup", Code: exitHalted}},
		{ID: "GH-2", Outcome: &drives.Outcome{Row: "halted at catchup", Code: exitHalted}},
		{ID: "GH-3", Skill: "ship"},
	}}

	if err := drives.Create(root, rec); err != nil {
		t.Fatal(err)
	}

	finished := func(row string) bool { return row == "GH-1" || row == "GH-3" }

	var list strings.Builder
	if err := driveList(&list, root, false, finished); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"GH-1:  finished", "GH-2:  halted at catchup", "GH-3:  finished"} {
		if !strings.Contains(list.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, list.String())
		}
	}

	got, err := drives.Read(root, 1)
	if err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string]string{"GH-1": rowFinished, "GH-2": "halted at catchup", "GH-3": rowFinished} {
		if o := got.Task(id).Outcome; o == nil || o.Row != want {
			t.Errorf("%s recorded %+v, want %q", id, o, want)
		}
	}

	if got.Task("GH-3").Skill != "" {
		t.Errorf("GH-3 kept its skill %q", got.Task("GH-3").Skill)
	}
}

// A running drive owns its record: list checks none of its rows.
func TestDriveListLeavesARunningDrive(t *testing.T) {
	root := t.TempDir()
	rec := &drives.Record{Args: []string{"GH-1"}, Tasks: []drives.Task{
		{ID: "GH-1", Outcome: &drives.Outcome{Row: "halted at catchup", Code: exitHalted}},
	}}

	if err := drives.Create(root, rec); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(drives.Path(root, 1))
	if err != nil {
		t.Fatal(err)
	}

	own, err := drives.Acquire(root, drives.DriveKey(1), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer own.Release()

	var list strings.Builder
	if err := driveList(&list, root, false, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(list.String(), "GH-1:  halted at catchup") {
		t.Errorf("list:\n%s", list.String())
	}

	if after, _ := os.ReadFile(drives.Path(root, 1)); !bytes.Equal(after, before) {
		t.Errorf("record changed:\n%s", after)
	}
}

func TestParseDriveSubcommands(t *testing.T) {
	cases := []struct {
		args []string
		want driveArgs
		err  string
	}{
		{args: []string{"list"}, want: driveArgs{list: true}},
		{args: []string{"list", "--all"}, want: driveArgs{list: true, all: true}},
		{args: []string{"list", "GH-1"}, err: "only --all"},
		{args: []string{"cancel", "3"}, want: driveArgs{cancel: 3}},
		{args: []string{"cancel"}, err: "one drive number"},
		{args: []string{"cancel", "x"}, err: "not a drive number"},
		{args: []string{"--resume", "2", "--answer", "1. yes"}, want: driveArgs{
			resume: 2, answers: []string{"1. yes"}, every: defaultEvery,
		}},
		{args: []string{"--resume=2", "GH-1"}, err: "takes only --answer, --every and --parallel"},
		{args: []string{"--resume", "2", "--parallel=3"}, want: driveArgs{resume: 2, parallel: 3, every: defaultEvery}},
	}

	for _, c := range cases {
		got, err := parseDriveArgs(c.args)

		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want %q", c.args, err, c.err)
			}
		case err != nil:
			t.Errorf("%q: %v", c.args, err)
		case got.list != c.want.list || got.all != c.want.all || got.cancel != c.want.cancel ||
			got.resume != c.want.resume || got.parallel != c.want.parallel || !slices.Equal(got.answers, c.want.answers):
			t.Errorf("%q = %+v, want %+v", c.args, got, c.want)
		}
	}
}

func TestRecordArgs(t *testing.T) {
	got := recordArgs(
		[]string{
			"GH-1", "--answer", "1. x", "--every=5s", "--flow", "local", "--resume", "2",
			"--parallel", "3", "GH-2:opus", "--parallel=4",
		},
	)
	if want := []string{"GH-1", "--flow", "local", "GH-2:opus"}; !slices.Equal(got, want) {
		t.Errorf("recordArgs = %q, want %q", got, want)
	}
}

// A drive is the launch of each session that types it, the rerun of a
// stopped drive from a new session included; a --resume or an --answer
// relay is nobody's. The record keeps the session that created it.
func TestOpenRecordRecordsTheLaunch(t *testing.T) {
	root, stateDir := t.TempDir(), t.TempDir()

	open := func(session string, change func(*driveInput)) *driveInput {
		in := &driveInput{root: root, stateDir: stateDir, session: session, argv: []string{"GH-1"}, now: time.Now}
		if change != nil {
			change(in)
		}

		if err := in.openRecord(nil); err != nil {
			t.Fatal(err)
		}

		if err := in.recordLaunch(); err != nil {
			t.Fatal(err)
		}

		in.releaseAll()

		return in
	}

	if in := open("sess-1", nil); in.rec.Session != "sess-1" {
		t.Errorf("record session = %q", in.rec.Session)
	}

	if in := open("sess-2", nil); in.rec.Session != "sess-1" {
		t.Errorf("a rerun rewrote the record's session to %q", in.rec.Session)
	}

	open("sess-3", func(in *driveInput) { in.args.resume = 1 })
	open("sess-4", func(in *driveInput) { in.args.answers = []string{"1. yes"} })

	for session, want := range map[string]string{"sess-1": "drive 1\n", "sess-2": "drive 1\n", "sess-3": "", "sess-4": ""} {
		b, err := os.ReadFile(state.LaunchesPath(stateDir, session))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}

		if got := string(b); got != want {
			t.Errorf("%s launches = %q, want %q", session, got, want)
		}
	}
}

// The cap a drive starts with is kept in its record: a rerun without
// --parallel keeps it, one with a new cap replaces it, and a drive started
// with none takes the default.
func TestOpenRecordKeepsTheCap(t *testing.T) {
	root := t.TempDir()

	open := func(argv []string, parallel int) *driveInput {
		in := &driveInput{
			root: root, stateDir: t.TempDir(), argv: argv, now: time.Now, defaultParallel: 6,
			args: driveArgs{parallel: parallel},
		}
		if err := in.openRecord(nil); err != nil {
			t.Fatal(err)
		}

		in.releaseAll()

		return in
	}

	for _, c := range []struct {
		argv     []string
		parallel int
		want     int
	}{
		{[]string{"GH-1", "--parallel", "3"}, 3, 3},
		{[]string{"GH-1"}, 0, 3},
		{[]string{"GH-1", "--parallel=5"}, 5, 5},
		{[]string{"GH-1"}, 0, 5},
		{[]string{"GH-2"}, 0, 6},
	} {
		in := open(c.argv, c.parallel)
		if in.parallel != c.want {
			t.Errorf("%q: cap %d, want %d", c.argv, in.parallel, c.want)
		}

		rec, err := drives.Read(root, in.rec.Number)
		if err != nil {
			t.Fatal(err)
		}

		if rec.Parallel != c.want {
			t.Errorf("%q: record keeps %d, want %d", c.argv, rec.Parallel, c.want)
		}
	}
}

func TestTaskStandingQueued(t *testing.T) {
	if got := taskStanding(drives.Task{Queued: true}, true); got != "queued" {
		t.Errorf("a queued task in a running drive stands %q", got)
	}

	if got := taskStanding(drives.Task{Queued: true}, false); got != "not reached" {
		t.Errorf("a queued task in a stopped drive stands %q", got)
	}
}
