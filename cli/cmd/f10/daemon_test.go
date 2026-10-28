package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/drives"
	"github.com/amberpixels/f10/cli/internal/state"
)

// fastTurn scripts a judge sent to GH-1 whose agent Herdr never shows
// working: its turn begins and ends between two looks.
func fastTurn(t *testing.T) *driveFixture {
	t.Helper()

	fx := newDriveFixture(t, "GH-1", "judge")
	fx.task("GH-1", taskInfo{body: "x"}, "")
	fx.f.script(worktrees, porcelain(), porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.script(wsList, unopened, listing("GH-1", "done"))

	return fx
}

// FT-132's fast turn: the prompt mark the hook left proves the agent took
// the prompt, so the turn reads as done, not as never taken.
func TestDriveSeesATurnBetweenTwoLooks(t *testing.T) {
	fx := fastTurn(t)
	fx.onTick[1] = func() {
		at := fx.clock.Add(-defaultEvery + time.Second)
		if err := state.MarkPrompt(fx.in.stateDir, "s-1", "/code/repo.GH-1", at); err != nil {
			t.Fatal(err)
		}
	}

	if err := fx.drive(t); err != nil {
		t.Fatalf("err = %v\n%s", err, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "GH-1 judge: done") {
		t.Errorf("output = %s", fx.out.String())
	}
}

// Without a mark, the same turn is still one never taken, and a mark in
// another checkout proves nothing.
func TestDriveWithoutAPromptMarkHalts(t *testing.T) {
	fx := fastTurn(t)
	fx.onTick[1] = func() {
		_ = state.MarkPrompt(fx.in.stateDir, "s-2", "/code/repo.GH-2", fx.clock)
	}

	err := fx.drive(t)
	if exitCode(err) != exitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, exitHalted, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "did not take the prompt") {
		t.Errorf("output = %s", fx.out.String())
	}
}

// A drive drained for an upgrade stops at its next tick, ends no task and
// keeps its record for the new daemon's resume.
func TestDriveDrainsAtATick(t *testing.T) {
	fx := fastTurn(t)
	fx.in.root = t.TempDir()
	fx.onTick[1] = func() { fx.in.draining.Store(true) }

	err := fx.drive(t)
	if exitCode(err) != exitDrained {
		t.Fatalf("err = %v, want exit %d\n%s", err, exitDrained, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "drive 1 drained for an upgrade") {
		t.Errorf("output = %s", fx.out.String())
	}

	rec, err := drives.Read(fx.in.root, 1)
	if err != nil {
		t.Fatal(err)
	}

	if rec.Tasks[0].Outcome != nil {
		t.Errorf("drained task has outcome %+v", rec.Tasks[0].Outcome)
	}

	if _, held := drives.Probe(fx.in.root, "GH-1"); held {
		t.Error("the task's lease is still held after the drain")
	}
}

func TestWakeReturnsOnAWrite(t *testing.T) {
	wakeStep = 10 * time.Millisecond
	t.Cleanup(func() { wakeStep = time.Second })

	in := &driveInput{stateDir: t.TempDir()}
	in.runs = []*driveRun{{opened: true, o: openedTask{path: "/code/repo.GH-1"}}}

	write := func(name, root string) {
		time.Sleep(50 * time.Millisecond)

		_ = os.WriteFile(filepath.Join(in.stateDir, name), []byte("v 1\nroot "+root+"\n"), 0o600)
	}

	go write("s1.state", "/code/repo.GH-1")

	start := time.Now()
	if err := in.wake(t.Context(), 5*time.Second); err != nil {
		t.Fatal(err)
	}

	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("wake took %s with a write after 50ms", took)
	}

	// another session's run, and a file no session owns, wake nothing
	go write("s2.state", "/code/elsewhere")
	go write("hooks.log", "/code/repo.GH-1")

	start = time.Now()
	if err := in.wake(t.Context(), 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	if took := time.Since(start); took < 290*time.Millisecond {
		t.Errorf("wake returned after %s with only other writes", took)
	}
}

func TestDriveEnv(t *testing.T) {
	got := driveEnv([]string{
		"PATH=/bin", "HOME=/h", "GH_TOKEN=x", "HERDR_ENV=1", "AWS_PROFILE=p", "SECRET=s", "LC_ALL=C", "ZIG=z",
	}, []string{"ZIG"})

	want := []string{"PATH=/bin", "HOME=/h", "GH_TOKEN=x", "HERDR_ENV=1", "LC_ALL=C", "ZIG=z"}
	if !slices.Equal(got, want) {
		t.Errorf("driveEnv = %v, want %v", got, want)
	}
}

func TestRecordArgsDropsForeground(t *testing.T) {
	if got := recordArgs([]string{"--foreground", "GH-1", "--every", "5s"}); !slices.Equal(got, []string{"GH-1"}) {
		t.Errorf("recordArgs = %v", got)
	}

	a, err := parseDriveArgs([]string{"GH-1", "--foreground"})
	if err != nil || !a.fore {
		t.Errorf("parse = %+v, %v", a, err)
	}
}

// The tail copies a log written in chunks, and ends with its exit code
// once the exit file appears; the log's drive line names the drive.
func TestTailLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.log")
	if err := os.WriteFile(path, []byte("drive 7\nGH-1 judge: sent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)

		f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		_, _ = f.WriteString("GH-1 judge: done\n")
		_ = f.Close()

		_ = os.WriteFile(daemon.ExitPath(path), []byte("6 7 upgrade\n"), 0o600)
	}()

	var out bytes.Buffer

	exit, err := tailLog(t.Context(), &out, path)
	if err != nil || exit != (daemon.Exit{Code: 6, Drive: 7, Reason: daemon.ReasonUpgrade}) {
		t.Fatalf("tail = %+v, %v", exit, err)
	}

	if out.String() != "drive 7\nGH-1 judge: sent\nGH-1 judge: done\n" {
		t.Errorf("tail copied %q", out.String())
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := os.Remove(daemon.ExitPath(path)); err != nil {
		t.Fatal(err)
	}

	if _, err := tailLog(ctx, &bytes.Buffer{}, path); err == nil {
		t.Error("a cancelled tail of a running drive returned no error")
	}
}

func TestDriveTitle(t *testing.T) {
	for root, want := range map[string]string{
		"/code/repo/.f10":     "repo: drive 3",
		"/home/u/.f10/widget": "widget: drive 3",
	} {
		if got := driveTitle(daemon.Run{Root: root, Drive: 3}); got != want {
			t.Errorf("driveTitle(%s) = %q, want %q", root, got, want)
		}
	}
}
