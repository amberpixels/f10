package main

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// broken is git failing to read rather than finding nothing: a lock, a
// broken worktree path, a killed process.
var broken = shell.Result{Code: 128, Stderr: "fatal: unable to read config"}

// A catchup whose mark or merge state git cannot read is not settled: a
// failed read never counts as "nothing left".
func TestUnsettledWhenGitCannotRead(t *testing.T) {
	for name, failing := range map[string]string{
		"mark":       "git config --get branch.GH-1." + cfgLanded,
		"MERGE_HEAD": "git rev-parse -q --verify MERGE_HEAD",
	} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "finish")
			fx.f.script("git config --get branch.GH-1."+cfgLanded, "")
			fx.f.script("git config --get branch.GH-1."+cfgMoved, "")
			fx.f.script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.script("git rev-parse -q --verify REBASE_HEAD", "")
			fx.f.script("git status --porcelain", "")
			fx.f.fail(failing, broken)

			r := &driveRun{o: openedTask{branch: "GH-1", path: "/code/repo.GH-1"}}

			if got := fx.in.unsettled(t.Context(), r); !strings.Contains(got, "unable to read config") {
				t.Errorf("unsettled = %q, want the failed read named", got)
			}
		})
	}
}

// finish never pushes, nor merges, on a branch whose origin copy git cannot
// read: a wrong "not pushed" or "not an ancestor" would pick the wrong push.
func TestDriveHaltsFinishWhenGitCannotRead(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "finish")
	fx.task("GH-1", taskInfo{body: "x"}, heads("GH-1"))
	fx.f.script(worktrees, porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.script(headRef, "origin/main")
	fx.f.script("git fetch origin", "")
	fx.f.fail("git rev-parse -q --verify origin/GH-1", broken)
	fx.f.script(wsList, unopened, listing("GH-1", "idle"))

	err := fx.drive(t)

	if exitCode(err) != exitHalted || fx.f.called("git push") || fx.f.called("finish GH-1") {
		t.Fatalf("err = %v, calls %q\n%s", err, fx.f.calls, fx.out.String())
	}
}

// The janitor keeps a branch whose upstream git cannot read, and says why.
func TestJanitorKeepsABranchItCannotRead(t *testing.T) {
	f := newFakes(t)
	f.fail("git config --get branch.GH-1.merge", broken)

	j := &sweeper{in: janitorInput{main: "/code/repo"}, def: "main"}

	if _, removed := j.sweepUnpushed(t.Context(), staleBranch{name: "GH-1", localSHA: "abc"}); removed {
		t.Fatal("removed a branch whose upstream could not be read")
	}

	if len(j.lines) != 1 || !strings.Contains(j.lines[0], "GH-1 kept") {
		t.Errorf("lines = %q, want one naming the kept branch", j.lines)
	}

	if f.called("git branch -D") {
		t.Errorf("deleted the branch: %q", f.calls)
	}
}
