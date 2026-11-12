package stack_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A branch linked to the task wins over the name patterns, local or on
// origin; two linked is an error; a suffix skips the link.
func TestFindPrefersLinksThenOrigin(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Meta.Seed("alice/fix", branchmeta.Task, "GH-1")
	f.Meta.Seed("other", branchmeta.Task, "GH-2")
	f.Script(testkit.RefsLocal, testkit.Heads("GH-1"))

	got, err := testkit.IndexOf(t).Find(t.Context(), "GH-1", "GH-1", "", stack.PolicyLocalOrOrigin)
	if err != nil || got != (stack.FoundBranch{Name: "alice/fix"}) {
		t.Errorf("a linked branch = %+v, %v; want alice/fix", got, err)
	}

	f.Script(testkit.RefsLocal, testkit.Heads("GH-1", "GH-1-v2"))

	got, err = testkit.IndexOf(t).Find(t.Context(), "GH-1-v2", "GH-1", "v2", stack.PolicyLocalOrOrigin)
	if err != nil || got.Name != "GH-1-v2" {
		t.Errorf("a suffix = %+v, %v; want the link skipped", got, err)
	}

	f.Meta.Seed("a", branchmeta.Task, "GH-1")
	f.Meta.Seed("b", branchmeta.Task, "gh-1")

	if _, err = testkit.IndexOf(t).Find(t.Context(), "GH-1", "GH-1", "", stack.PolicyLocalOrOrigin); err == nil {
		t.Error("two linked branches were not an error")
	}

	f.Meta.Clear()
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.RefsOrigin, "refs/remotes/origin/GH-1/a\nrefs/remotes/origin/GH-1/b\n")

	if _, err = testkit.IndexOf(t).Find(t.Context(), "GH-1", "GH-1", "", stack.PolicyLocalOrOrigin); err == nil {
		t.Error("two task branches on origin were not an error")
	}

	f.Script(testkit.RefsOrigin, "refs/remotes/origin/GH-1/a\n")

	got, err = testkit.IndexOf(t).Find(t.Context(), "GH-1", "GH-1", "", stack.PolicyLocalOrOrigin)
	if err != nil || got != (stack.FoundBranch{Name: "GH-1/a", Remote: true}) {
		t.Errorf("an origin-only branch = %+v, %v", got, err)
	}

	// a branch only origin has is checked out nowhere: nothing to forward to
	f.Script(testkit.Worktrees, testkit.Porcelain())

	name, path, err := stack.TaskCheckout(t.Context(), "/code/repo", nil, testkit.Gh1, "")
	if name != "" || path != "" || err != nil {
		t.Errorf("taskCheckout on an origin-only branch = %q, %q, %v", name, path, err)
	}
}

// A local name carrying the id is not the task's branch when its link names
// another task; the local policy never looks at origin.
func TestLocalPolicySkipsForeignLinksAndOrigin(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Meta.Seed("feat/GH-1-fix", branchmeta.Task, "GH-9")
	f.Script(testkit.RefsLocal, "")
	f.Script(testkit.AllHeads, "refs/heads/feat/GH-1-fix\nrefs/heads/feat/GH-1-other\n")

	got, err := testkit.IndexOf(t).Find(t.Context(), "GH-1", "GH-1", "", stack.PolicyLocal)
	if err != nil || got != (stack.FoundBranch{Name: "feat/GH-1-other"}) {
		t.Errorf("a name linked to another task = %+v, %v; want feat/GH-1-other alone", got, err)
	}

	f.Script(testkit.AllHeads, "")
	f.Script(testkit.RefsOrigin, "refs/remotes/origin/GH-1/a\n")

	got, err = testkit.IndexOf(t).Find(t.Context(), "GH-1", "GH-1", "", stack.PolicyLocal)
	if err != nil || got != (stack.FoundBranch{}) {
		t.Errorf("an origin-only branch under the local policy = %+v, %v; want none", got, err)
	}

	if f.Called(testkit.RefsOrigin) {
		t.Error("the local policy listed origin's branches")
	}
}

func TestLinkedBranchCountsForAfter(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Meta.Seed("alice/base", branchmeta.Task, "GH-7")

	if got, err := testkit.IndexOf(t).After(t.Context(), "GH-7"); err != nil || got != "alice/base" {
		t.Errorf("after on a linked branch = %q, %v", got, err)
	}

	if ok, err := stack.HasTaskBranch(t.Context(), "/code/repo", "GH-7"); !ok || err != nil {
		t.Errorf("hasTaskBranch on a linked branch = %v, %v", ok, err)
	}
}
