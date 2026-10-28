package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/drives"
)

// inboxFixture is a drive fixture with a record and an inbox, counting
// notifications and resumes.
func inboxFixture(t *testing.T, args ...string) (*driveFixture, *int, *[]int) {
	t.Helper()

	fx := newDriveFixture(t, args...)
	fx.in.root, fx.in.asksDir = t.TempDir(), t.TempDir()

	notified, resumed := 0, []int{}
	fx.in.notify = func(context.Context, asks.Ask) error {
		notified++

		return nil
	}
	fx.in.resume = func(_ context.Context, n int) error {
		resumed = append(resumed, n)

		return nil
	}

	return fx, &notified, &resumed
}

// asking scripts GH-1 sent judge, then stopping on a question.
func (fx *driveFixture) asking(t *testing.T) {
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" "+skillPrompt("judge", "GH-1", nil, shipFlow{}, false), "{}")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]") }
}

// resumed scripts a rerun finding GH-1 stopped on its question.
func (fx *driveFixture) resumedOnAsk(t *testing.T) {
	fx.adoptsGH1()
	fx.writeRun(t, "blocked", "judge", "", "1. Rethink: [proceed | stop]")
	fx.f.script("git config branch.GH-1."+cfgDrive+" judge", "")
	fx.f.script(wsList, unopened, listing("GH-1", "idle"), listing("GH-1", "working"), listing("GH-1", "idle"))
	fx.onTick[2] = func() { fx.writeRun(t, "done", "", "", "") }
}

// A task that asks puts its question in the inbox, naming the drive, and
// the user is told once.
func TestDriveRaisesAskIntoInbox(t *testing.T) {
	fx, notified, resumed := inboxFixture(t, "GH-1", "judge")
	fx.asking(t)

	if err := fx.drive(t); exitCode(err) != exitAsk {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	open, err := asks.Open(fx.in.asksDir)
	if err != nil || len(open) != 1 {
		t.Fatalf("open asks = %+v, %v", open, err)
	}

	a := open[0]
	if a.Task != "GH-1" || a.Drive != 1 || a.Skill != "judge" || a.Worktree != "/code/repo.GH-1" ||
		len(a.Questions) != 1 || a.Questions[0].Default() != "proceed" {
		t.Errorf("ask = %+v", a)
	}

	if *notified != 1 || len(*resumed) != 0 {
		t.Errorf("notified %d, resumed %v", *notified, *resumed)
	}
}

// An answer given through the inbox reaches the agent on the resume, as an
// --answer would, and the ask reads taken.
func TestDriveTakesInboxAnswer(t *testing.T) {
	fx, _, _ := inboxFixture(t, "GH-1", "judge")
	fx.resumedOnAsk(t)
	fx.f.script("herdr agent prompt "+agentOf("GH-1")+" 1. stop", "{}")

	a, _, _ := asks.Raise(fx.in.asksDir, asks.Ask{
		Task: "GH-1", Root: fx.in.root, Drive: 1,
		Worktree: "/code/repo.GH-1", Text: "1. Rethink: [proceed | stop]",
	}, time.Now())
	if _, err := asks.Claim(fx.in.asksDir, a.Key(), asks.ByAsks, "1. stop", false, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "GH-1 judge: answer sent") {
		t.Errorf("output:\n%s", fx.out.String())
	}

	got, _, _ := asks.Get(fx.in.asksDir, a.Key())
	if got.TakenAt.IsZero() {
		t.Error("the inbox answer was not marked taken")
	}

	rec, _ := drives.Read(fx.in.root, 1)
	if len(rec.Answered) != 1 || rec.Answered[0].Text != "1. stop" {
		t.Errorf("record answered = %+v", rec.Answered)
	}
}

// An answer typed in the agent's pane already reached it: the resume sends
// nothing and follows the turn.
func TestDriveFollowsPaneAnswer(t *testing.T) {
	fx, _, _ := inboxFixture(t, "GH-1", "judge")
	fx.resumedOnAsk(t)

	a, _, _ := asks.Raise(fx.in.asksDir, asks.Ask{
		Task: "GH-1", Root: fx.in.root, Drive: 1,
		Worktree: "/code/repo.GH-1", Text: "1. Rethink: [proceed | stop]",
	}, time.Now())
	if _, err := asks.Claim(fx.in.asksDir, a.Key(), asks.ByPane, "stop", true, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("%v\n%s", err, fx.out.String())
	}

	if fx.f.called("herdr agent prompt") {
		t.Errorf("a pane answer was sent again: %v", fx.f.calls)
	}

	if out := fx.out.String(); !strings.Contains(out, "following the turn") || !strings.Contains(out, "done: judge") {
		t.Errorf("output:\n%s", out)
	}
}

// An answer claimed while the drive still held its lease is not lost: the
// drive, once it lets go, resumes itself for it.
func TestDriveResumesForLateAnswer(t *testing.T) {
	fx, _, resumed := inboxFixture(t, "GH-1", "judge")
	fx.asking(t)

	// the user answers the moment they are told, before the drive exits
	fx.in.notify = func(_ context.Context, a asks.Ask) error {
		if _, held := drives.Probe(fx.in.root, drives.DriveKey(1)); !held {
			t.Error("the drive's lease was free before it exited")
		}

		_, err := asks.Claim(fx.in.asksDir, a.Key(), asks.ByAsks, "1. proceed", false, time.Now())

		return err
	}

	if err := fx.drive(t); exitCode(err) != exitAsk {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	if len(*resumed) != 1 || (*resumed)[0] != 1 {
		t.Errorf("resumed %v, want drive 1", *resumed)
	}
}

// A cancelled drive leaves nothing waiting in the inbox.
func TestCloseDriveAsks(t *testing.T) {
	dir, now := t.TempDir(), time.Now()

	for _, a := range []asks.Ask{
		{Task: "GH-1", Root: "/r", Drive: 2, Text: "1. x? [a | b]"},
		{Task: "GH-2", Root: "/r", Drive: 3, Text: "1. y? [a | b]"},
	} {
		if _, _, err := asks.Raise(dir, a, now); err != nil {
			t.Fatal(err)
		}
	}

	closeDriveAsks(dir, "/r", 2, now)

	open, _ := asks.Open(dir)
	if len(open) != 1 || open[0].Task != "GH-2" {
		t.Errorf("open = %+v", open)
	}

	if left, _ := asks.Untaken(dir, "/r"); len(left) != 0 {
		t.Errorf("a cancelled ask waits to be taken: %+v", left)
	}
}
