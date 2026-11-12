package stack_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A base whose only commit is a merge of its own base's newer head, as the
// catchup guard before implement leaves one, has no code: drive 6 released
// two dependents on such a merge.
func TestBaseHasCodeIgnoresMerges(t *testing.T) {
	r := testkit.NewCatchupRepo(t)

	// FT-1/a is the base's base; FT-2/b stacks on it and merges its next commit
	r.At(testkit.Before, "checkout", "-q", "-b", "FT-2/b", "FT-1/a")
	r.At(testkit.Before, "config", branchmeta.AfterBranch.For("FT-2/b"), "FT-1/a")
	r.At(testkit.Before, "checkout", "-q", "FT-1/a")
	r.Commit(testkit.During, "a2.go", "package main // a2\n", "a2")
	r.At(testkit.During, "checkout", "-q", "FT-2/b")
	r.At(testkit.During, "merge", "-q", "--no-ff", "--no-edit", "FT-1/a")

	if stack.BaseHasCode(t.Context(), r.Dir, "FT-2/b", "main") {
		t.Error("a merge of its own base counted as code")
	}

	r.Commit(testkit.During, "b.go", "package main // b2\n", "b")

	if !stack.BaseHasCode(t.Context(), r.Dir, "FT-2/b", "main") {
		t.Error("a commit of its own did not count")
	}
}

// A base only origin holds is read there by the same rule: its merge of its
// own base is not code, a commit of its own is.
func TestOriginBaseHasCode(t *testing.T) {
	r := testkit.NewCatchupRepo(t)

	r.At(testkit.Before, "checkout", "-q", "-b", "FT-2/b", "FT-1/a")
	r.At(testkit.Before, "config", branchmeta.AfterBranch.For("FT-2/b"), "FT-1/a")
	r.At(testkit.Before, "checkout", "-q", "FT-1/a")
	r.Commit(testkit.During, "a2.go", "package main // a2\n", "a2")
	r.At(testkit.During, "checkout", "-q", "FT-2/b")
	r.At(testkit.During, "merge", "-q", "--no-ff", "--no-edit", "FT-1/a")
	r.At(testkit.During, "update-ref", "refs/remotes/origin/FT-2/b", "HEAD")
	r.At(testkit.During, "update-ref", "refs/remotes/origin/FT-1/a", "FT-1/a")

	if stack.OriginBaseHasCode(t.Context(), r.Dir, "FT-2/b", "main") {
		t.Error("origin's merge of its own base counted as code")
	}

	r.Commit(testkit.During, "b.go", "package main // b2\n", "b")
	r.At(testkit.During, "update-ref", "refs/remotes/origin/FT-2/b", "HEAD")

	if !stack.OriginBaseHasCode(t.Context(), r.Dir, "FT-2/b", "main") {
		t.Error("origin's commit of its own did not count")
	}
}
