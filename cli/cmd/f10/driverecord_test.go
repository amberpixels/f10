package main

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drives"
)

// A task another drive holds is skipped, naming that drive: nothing is
// opened or prompted for it.
func TestDriveSkipsATaskAnotherDriveOwns(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "judge")
	fx.in.root = t.TempDir()
	fx.task("GH-1", taskInfo{body: "x"}, "")

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
	if err := driveList(&list, fx.in.root, false); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"drive 1 ", "  cancelled\n", "f10 drive GH-1 judge ship", "└─ GH-1:  cancelled"} {
		if !strings.Contains(list.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, list.String())
		}
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
		{args: []string{"--resume=2", "GH-1"}, err: "takes only --answer and --every"},
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
			got.resume != c.want.resume || !slices.Equal(got.answers, c.want.answers):
			t.Errorf("%q = %+v, want %+v", c.args, got, c.want)
		}
	}
}

func TestRecordArgs(t *testing.T) {
	got := recordArgs(
		[]string{"GH-1", "--answer", "1. x", "--every=5s", "--flow", "local", "--resume", "2", "GH-2:opus"},
	)
	if want := []string{"GH-1", "--flow", "local", "GH-2:opus"}; !slices.Equal(got, want) {
		t.Errorf("recordArgs = %q, want %q", got, want)
	}
}
