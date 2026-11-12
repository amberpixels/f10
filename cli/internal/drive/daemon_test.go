package drive

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
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// fastTurn scripts a judge sent to GH-1 whose agent Herdr never shows
// working: its turn begins and ends between two looks.
func fastTurn(t *testing.T) *driveFixture {
	t.Helper()

	fx := newDriveFixture(t, "GH-1", "judge")
	fx.task("GH-1", tasksource.TaskInfo{Body: "x"}, "")
	fx.f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.expects("GH-1", "judge")
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "done"))

	return fx
}

// FT-132's fast turn: the prompt mark the hook left proves the agent took
// the prompt, so the turn reads as done, not as never taken.
func TestDriveSeesATurnBetweenTwoLooks(t *testing.T) {
	fx := fastTurn(t)
	fx.onTick[1] = func() {
		at := fx.clock.Add(-drivectl.DefaultEvery + time.Second)
		if err := state.MarkPrompt(fx.in.StateDir, "s-1", "/code/repo.GH-1", at); err != nil {
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
		_ = state.MarkPrompt(fx.in.StateDir, "s-2", "/code/repo.GH-2", fx.clock)
	}

	err := fx.drive(t)
	if testkit.ExitCode(err) != ui.ExitHalted {
		t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitHalted, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "did not take the prompt") {
		t.Errorf("output = %s", fx.out.String())
	}
}

// A drive drained for an upgrade stops at its next tick, ends no task and
// keeps its record for the new daemon's resume.
func TestDriveDrainsAtATick(t *testing.T) {
	fx := fastTurn(t)
	fx.in.Root = t.TempDir()
	fx.onTick[1] = func() { fx.in.Draining.Store(true) }

	err := fx.drive(t)
	if testkit.ExitCode(err) != daemon.ExitDrained {
		t.Fatalf("err = %v, want exit %d\n%s", err, daemon.ExitDrained, fx.out.String())
	}

	if !strings.Contains(fx.out.String(), "drive 1 drained for an upgrade") {
		t.Errorf("output = %s", fx.out.String())
	}

	rec, err := drivestore.Read(fx.in.Root, 1)
	if err != nil {
		t.Fatal(err)
	}

	if rec.Tasks[0].Outcome != nil {
		t.Errorf("drained task has outcome %+v", rec.Tasks[0].Outcome)
	}

	if _, held := drivestore.Probe(fx.in.Root, "GH-1"); held {
		t.Error("the task's lease is still held after the drain")
	}
}

func TestWakeReturnsOnAWrite(t *testing.T) {
	in := &DriveInput{}
	in.StateDir, in.WakeStep = t.TempDir(), 10*time.Millisecond
	in.Runs = []*DriveRun{{Phase: atRunning, O: start.OpenedTask{Path: "/code/repo.GH-1"}}}

	write := func(name, root string) {
		time.Sleep(50 * time.Millisecond)

		_ = os.WriteFile(filepath.Join(in.StateDir, name), []byte("v 1\nroot "+root+"\n"), 0o600)
	}

	go write("s1.state", "/code/repo.GH-1")

	started := time.Now()
	if err := in.Wake(t.Context(), 5*time.Second); err != nil {
		t.Fatal(err)
	}

	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("wake took %s with a write after 50ms", took)
	}

	// another session's run, and a file no session owns, wake nothing
	go write("s2.state", "/code/elsewhere")
	go write("hooks.log", "/code/repo.GH-1")

	started = time.Now()
	if err := in.Wake(t.Context(), 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	if took := time.Since(started); took < 290*time.Millisecond {
		t.Errorf("wake returned after %s with only other writes", took)
	}
}

func TestRecordArgsDropsForeground(t *testing.T) {
	if got := recordArgs(
		[]string{"--foreground", "GH-1", "--every", "5s"},
	); !slices.Equal(
		got,
		[]string{"GH-1"},
	) {
		t.Errorf("recordArgs = %v", got)
	}

	a, err := drivectl.ParseDriveArgs([]string{"GH-1", "--foreground"})
	if err != nil || !a.Fore {
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

	exit, err := TailLog(t.Context(), &out, path)
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

	if _, err := TailLog(ctx, &bytes.Buffer{}, path); err == nil {
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
