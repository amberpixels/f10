package drive

import (
	"errors"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A catchup whose mark or merge state git cannot read is not settled: a
// failed read never counts as "nothing left".
func TestUnsettledWhenGitCannotRead(t *testing.T) {
	for name, fail := range map[string]func(*testkit.Fakes){
		"mark": func(f *testkit.Fakes) { f.Meta.FailRead("GH-1", branchmeta.Landed, errors.New(testkit.Broken.Stderr)) },
		"MERGE_HEAD": func(f *testkit.Fakes) {
			f.Fail("git rev-parse -q --verify MERGE_HEAD", testkit.Broken)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newDriveFixture(t, "GH-1", "finish")

			fx.f.Script("git rev-parse -q --verify MERGE_HEAD", "")
			fx.f.Script("git status --porcelain", "")
			fail(fx.f)

			r := &DriveRun{O: start.OpenedTask{Branch: "GH-1", Path: "/code/repo.GH-1"}}

			if got := stack.BranchUnsettled(
				t.Context(),
				fx.in.Main,
				r.O.Path,
				r.O.Branch,
			); !strings.Contains(
				got,
				"unable to read config",
			) {
				t.Errorf("unsettled = %q, want the failed read named", got)
			}
		})
	}
}

// finish never pushes, nor merges, on a branch whose origin copy git cannot
// read: a wrong "not pushed" or "not an ancestor" would pick the wrong push.
func TestDriveHaltsFinishWhenGitCannotRead(t *testing.T) {
	fx := newDriveFixture(t, "GH-1", "finish")
	fx.task("GH-1", tasksource.TaskInfo{Body: "x"}, testkit.Heads("GH-1"))
	fx.f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", "/code/repo.GH-1"))
	fx.opens("GH-1", "ws:GH-1", "pane:1")
	fx.f.Script(testkit.HeadRef, "origin/main")
	fx.f.Script("git fetch origin", "")
	fx.f.Fail("git rev-parse -q --verify origin/GH-1", testkit.Broken)
	fx.f.Script(testkit.WsList, unopened, listing("GH-1", "idle"))

	err := fx.drive(t)

	if testkit.ExitCode(err) != ui.ExitHalted || fx.f.Called("git push") || fx.f.Called("finish GH-1") {
		t.Fatalf("err = %v, calls %q\n%s", err, fx.f.Calls, fx.out.String())
	}
}
