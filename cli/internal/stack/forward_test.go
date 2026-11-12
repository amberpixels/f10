package stack_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// TaskCheckout is the lookup forward, status and finish share: the branch
// as start would find it, and the checkout that has it, or neither.
func TestTaskCheckout(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))
	f.Script(testkit.Worktrees, testkit.Porcelain("GH-1", testkit.FwdWT))

	name, path, err := stack.TaskCheckout(t.Context(), testkit.FwdMain, nil, testkit.Gh1, "")
	if err != nil || name != "GH-1" || path != testkit.FwdWT {
		t.Errorf("taskCheckout = %q, %q, %v", name, path, err)
	}

	f.Script(testkit.Worktrees, testkit.Porcelain())

	if name, path, err = stack.TaskCheckout(
		t.Context(),
		testkit.FwdMain,
		nil,
		testkit.Gh1,
		"",
	); err != nil || name != "GH-1" ||
		path != "" {
		t.Errorf("a branch with no worktree = %q, %q, %v", name, path, err)
	}

	f.Script(testkit.RefsLocal, "")

	if name, path, err = stack.TaskCheckout(
		t.Context(),
		testkit.FwdMain,
		nil,
		testkit.Gh1,
		"",
	); err != nil || name != "" ||
		path != "" {
		t.Errorf("no branch = %q, %q, %v", name, path, err)
	}
}
