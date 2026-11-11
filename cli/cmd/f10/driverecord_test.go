package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A task another drive holds is skipped, naming that drive: nothing is
// opened or prompted for it.
func TestDriveSkipsATaskAnotherDriveOwns(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.in.Root = t.TempDir()
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")

	// bouncing off the lease says nothing about the owner's task
	fx.in.Record = func(_ context.Context, _ string, e ledger.Event) { t.Errorf("recorded %+v", e) }

	other, err := drives.Acquire(fx.in.Root, "GH-1", 7)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()

	err = fx.drive(t)
	if exitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitHalted, fx.out.String())
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
	fx.in.Root = root
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script(
		"herdr agent prompt "+agentOf("GH-1")+" "+drive.SkillPrompt("judge", "GH-1", nil, runplan.ShipFlow{}, false),
		"{}",
	)
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]") }

	if err := fx.drive(t); exitCode(err) != ui.ExitAsk {
		t.Fatalf("err = %v", err)
	}

	if want := `answer with: f10 drive --resume 1 --answer "1. … 2. …"`; !strings.Contains(fx.out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, fx.out.String())
	}

	rec, err := drives.Read(root, 1)
	if err != nil {
		t.Fatal(err)
	}

	if o := rec.Task("GH-1").Outcome; o == nil || o.Row != "asked at judge" || o.Code != ui.ExitAsk {
		t.Errorf("recorded outcome = %+v", o)
	}

	// the drive's leases went with it
	if _, held := drives.Probe(root, "GH-1"); held {
		t.Error("the task's lease outlived the drive")
	}

	fx = newDriveFixture(t, "GH-1", "judge", "--answer", "1. proceed")
	fx.in.Root = root
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, heads("GH-1"))
	fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	fx.f.script("herdr worktree open --cwd /code/repo --path /code/repo.GH-1 --label GH-1",
		`{"result":{"workspace":{"workspace_id":"ws:GH-1"},"root_pane":{"pane_id":"pane:1"},"already_open":true}}`)
	fx.f.script("herdr agent get pane:1", agentFound)
	fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" 1. proceed", "{}")

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
	fx.in.Root = t.TempDir()
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")
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

	rec, err := drives.Read(fx.in.Root, 1)
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

	in := &drive.DriveInput{
		Root: root,
		Argv: []string{"GH-1", "GH-2", "--answer", "GH-2=2. maybe"},
		Now:  time.Now,
	}
	in.Args.Answers = []string{"GH-2=2. maybe"}

	if err := in.OpenRecord(answerTasks("GH-1", "GH-2")); err != nil {
		t.Fatal(err)
	}
	defer in.ReleaseAll()

	if want := []string{"GH-1=1. yes", "GH-2=2. maybe"}; !slices.Equal(in.Args.Answers, want) {
		t.Errorf("answers = %q, want %q", in.Args.Answers, want)
	}

	// the drive's own lease is held: a second drive on the record refuses
	second := &drive.DriveInput{
		Root: root,
		Argv: []string{"GH-1", "GH-2"},
		Now:  time.Now,
	}
	if err := second.OpenRecord(nil); err == nil || !strings.Contains(err.Error(), "drive 1 is running") {
		t.Errorf("second openRecord = %v", err)
	}
}

// A bare answer whose text holds "=" stays bare: the rerun's replaces the
// kept one, or TakeAnswers sees two bare answers and refuses.
func TestOpenRecordReplacesABareAnswerHoldingEquals(t *testing.T) {
	root := t.TempDir()

	rec := &drives.Record{Args: []string{"GH-1"}, Answers: map[string]string{"": "1. old"}}
	if err := drives.Create(root, rec); err != nil {
		t.Fatal(err)
	}

	in := &drive.DriveInput{
		Root: root,
		Argv: []string{"GH-1", "--answer", "use x=1"},
		Now:  time.Now,
	}
	in.Args.Answers = []string{"use x=1"}

	tasks := answerTasks("GH-1")
	if err := in.OpenRecord(tasks); err != nil {
		t.Fatal(err)
	}
	defer in.ReleaseAll()

	if want := []string{"use x=1"}; !slices.Equal(in.Args.Answers, want) {
		t.Errorf("answers = %q, want %q", in.Args.Answers, want)
	}

	if err := in.TakeAnswers(tasks); err != nil || in.Answers.Bare != "use x=1" {
		t.Errorf("takeAnswers = %v, answer %q", err, in.Answers.Bare)
	}
}

func answerTasks(ids ...string) []drive.DriveTask {
	tasks := make([]drive.DriveTask, len(ids))
	for i, id := range ids {
		tasks[i] = drive.DriveTask{Ref: ref.Ref{ID: id}}
	}

	return tasks
}

// The agent got past a skill before the drive could send it, as after a
// drive stopped mid-ship: the skill is counted done, not prompted again.
func TestDriveSkipsAPromptTheAgentAlreadyFinished(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "plan", "ship")
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "plan")

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
	fx.in.Root = t.TempDir()
	fx.task("GH-1", hostops.TaskInfo{Body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"))
	fx.in.Record = func(_ context.Context, _ string, e ledger.Event) { t.Errorf("recorded %+v", e) }
	fx.onTick[1] = func() {
		var out strings.Builder
		if err := drive.DriveCancel(&out, fx.in.Root, 1, fx.clock); err != nil {
			t.Error(err)
		}

		if !strings.Contains(out.String(), "drive 1 cancelled: pid") {
			t.Errorf("cancel said %q", out.String())
		}
	}

	if err := fx.drive(t); exitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "GH-1:    cancelled") {
		t.Errorf("output:\n%s", fx.out.String())
	}

	if fx.f.called("herdr agent prompt "+agentOf("GH-1")+" /f10:ship") || fx.f.called("git worktree remove") {
		t.Errorf("acted after the cancel: %v", fx.f.calls)
	}

	var list strings.Builder
	if err := drive.DriveList(t.Context(), &list, nil, "", fx.in.Root, false, nil); err != nil {
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
		{ID: "GH-1", Outcome: &drives.Outcome{Row: "halted at catchup", Code: ui.ExitHalted}},
		{ID: "GH-2", Outcome: &drives.Outcome{Row: "halted at catchup", Code: ui.ExitHalted}},
		{ID: "GH-3", Skill: "ship"},
	}}

	if err := drives.Create(root, rec); err != nil {
		t.Fatal(err)
	}

	finished := func(row string) bool { return row == "GH-1" || row == "GH-3" }

	var list strings.Builder
	if err := drive.DriveList(t.Context(), &list, nil, "", root, false, finished); err != nil {
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

	for id, want := range map[string]string{"GH-1": driverec.RowFinished, "GH-2": "halted at catchup", "GH-3": driverec.RowFinished} {
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
		{ID: "GH-1", Outcome: &drives.Outcome{Row: "halted at catchup", Code: ui.ExitHalted}},
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
	if err := drive.DriveList(t.Context(), &list, nil, "", root, false, func(string) bool { return true }); err != nil {
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
		want driverec.DriveArgs
		err  string
	}{
		{args: []string{"list"}, want: driverec.DriveArgs{List: true}},
		{args: []string{"list", "--all"}, want: driverec.DriveArgs{List: true, All: true}},
		{args: []string{"list", "GH-1"}, err: "only --all"},
		{args: []string{"cancel", "3"}, want: driverec.DriveArgs{Cancel: 3}},
		{args: []string{"cancel"}, err: "one drive number"},
		{args: []string{"cancel", "x"}, err: "not a drive number"},
		{args: []string{"--resume", "2", "--answer", "1. yes"}, want: driverec.DriveArgs{
			Resume: 2, Answers: []string{"1. yes"}, Every: driverec.DefaultEvery,
		}},
		{args: []string{"--resume=2", "GH-1"}, err: "takes only --answer, --every and --parallel"},
		{
			args: []string{"--resume", "2", "--parallel=3"},
			want: driverec.DriveArgs{Resume: 2, Parallel: 3, Every: driverec.DefaultEvery},
		},
	}

	for _, c := range cases {
		got, err := driverec.ParseDriveArgs(c.args)

		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want %q", c.args, err, c.err)
			}
		case err != nil:
			t.Errorf("%q: %v", c.args, err)
		case got.List != c.want.List || got.All != c.want.All || got.Cancel != c.want.Cancel ||
			got.Resume != c.want.Resume || got.Parallel != c.want.Parallel || !slices.Equal(got.Answers, c.want.Answers):
			t.Errorf("%q = %+v, want %+v", c.args, got, c.want)
		}
	}
}

func TestRecordArgs(t *testing.T) {
	got := drive.RecordArgs(
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

	open := func(session string, change func(*drive.DriveInput)) *drive.DriveInput {
		in := &drive.DriveInput{
			Root:     root,
			StateDir: stateDir,
			Session:  session,
			Argv:     []string{"GH-1"},
			Now:      time.Now,
		}
		if change != nil {
			change(in)
		}

		if err := in.OpenRecord(nil); err != nil {
			t.Fatal(err)
		}

		if err := in.RecordLaunch(); err != nil {
			t.Fatal(err)
		}

		in.ReleaseAll()

		return in
	}

	if in := open("sess-1", nil); in.Rec.Session != "sess-1" {
		t.Errorf("record session = %q", in.Rec.Session)
	}

	if in := open("sess-2", nil); in.Rec.Session != "sess-1" {
		t.Errorf("a rerun rewrote the record's session to %q", in.Rec.Session)
	}

	open("sess-3", func(in *drive.DriveInput) { in.Args.Resume = 1 })
	open("sess-4", func(in *drive.DriveInput) { in.Args.Answers = []string{"1. yes"} })

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

	open := func(argv []string, parallel int) *drive.DriveInput {
		in := &drive.DriveInput{
			Root:            root,
			StateDir:        t.TempDir(),
			Argv:            argv,
			DefaultParallel: 6,
			Args:            driverec.DriveArgs{Parallel: parallel},
			Now:             time.Now,
		}
		if err := in.OpenRecord(nil); err != nil {
			t.Fatal(err)
		}

		in.ReleaseAll()

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
		if in.Parallel != c.want {
			t.Errorf("%q: cap %d, want %d", c.argv, in.Parallel, c.want)
		}

		rec, err := drives.Read(root, in.Rec.Number)
		if err != nil {
			t.Fatal(err)
		}

		if rec.Parallel != c.want {
			t.Errorf("%q: record keeps %d, want %d", c.argv, rec.Parallel, c.want)
		}
	}
}

func TestTaskStandingQueued(t *testing.T) {
	if got := drive.TaskStanding(drives.Task{Queued: true}, true); got != "queued" {
		t.Errorf("a queued task in a running drive stands %q", got)
	}

	if got := drive.TaskStanding(drives.Task{Queued: true}, false); got != "not reached" {
		t.Errorf("a queued task in a stopped drive stands %q", got)
	}
}

// A task its running drive parked, or holds at finish, stands waiting, not
// running the skill it last had in flight.
func TestTaskStandingWaiting(t *testing.T) {
	parked := drives.Task{Skill: "ship", Phase: drives.PhaseParked}
	if got := drive.TaskStanding(parked, true); got != "waiting on its base" {
		t.Errorf("a parked task stands %q", got)
	}

	held := drives.Task{Skill: "finish", Phase: drives.PhaseWaiting}
	if got := drive.TaskStanding(held, true); got != "waiting to finish after its base" {
		t.Errorf("a finish held for its base stands %q", got)
	}

	if got := drive.TaskStanding(parked, false); got != "stopped at ship" {
		t.Errorf("a parked task in a stopped drive stands %q", got)
	}
}

// drive list decides a row by FinishedTask, asking the tracker only when the
// branch and the plans dir leave it open.
func TestFinishedLazily(t *testing.T) {
	cases := []struct {
		name                     string
		branched, archived, live bool
		closed, wantInfo, want   bool
	}{
		{name: "closed, nothing on disk", closed: true, wantInfo: true, want: true},
		{name: "open, nothing on disk", wantInfo: true},
		{name: "branched", branched: true, closed: true},
		{name: "archived", archived: true, want: true},
		{name: "re-planned", archived: true, live: true, closed: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := storage.Root(t.TempDir())
			if c.archived {
				writePlan(t, filepath.Join(root.Archive(), "GH-1.md"))
			}

			if c.live {
				writePlan(t, root.Plan("GH-1"))
			}

			asked := false
			got := drive.FinishedLazily(root, "GH-1",
				func() (bool, error) { return c.branched, nil },
				func() hostops.TaskInfo { asked = true; return hostops.TaskInfo{Closed: c.closed} })

			if got != c.want || asked != c.wantInfo {
				t.Errorf("finishedLazily = %v, tracker asked %v; want %v, %v", got, asked, c.want, c.wantInfo)
			}
		})
	}
}

// The record names where a task stands as its phase moves: held by the cap,
// running, parked on its base, waiting at finish, ended; queued is kept
// beside the phase for readers that know only it.
func TestRecordFollowsThePhase(t *testing.T) {
	root := t.TempDir()

	rec := &drives.Record{Args: []string{"GH-1"}, Tasks: []drives.Task{{ID: "GH-1"}}}
	if err := drives.Create(root, rec); err != nil {
		t.Fatal(err)
	}

	in := &drive.DriveInput{
		Root: root,
		Out:  io.Discard,
		Rec:  rec,
	}
	r := &drive.DriveRun{Task: drive.DriveTask{Ref: ref.Ref{ID: "GH-1"}}}

	want := func(phase string, queued bool) {
		t.Helper()

		got, err := drives.Read(root, rec.Number)
		if err != nil {
			t.Fatal(err)
		}

		if task := got.Tasks[0]; task.Phase != phase || task.Queued != queued {
			t.Errorf("task = %+v, want phase %q queued %v", task, phase, queued)
		}
	}

	in.Queue(r)
	want(drives.PhaseQueued, true)

	in.Unqueue(r)
	in.Enter(r, drive.AtRunning)
	want(drives.PhaseRunning, false)

	in.Enter(r, drive.AtParked)
	want(drives.PhaseParked, false)

	in.Enter(r, drive.AtWaiting)
	want(drives.PhaseWaiting, false)

	in.End(t.Context(), r, drive.DriveResult{Row: "done: ship"})
	want(drives.PhaseEnded, false)
}
