package testkit

import (
	"os/exec"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/stack"
)

// NewCatchupRepo is a clone with main pushed and FT-1/a branched from it;
// origin's main then moves on, so FT-1/a has something to bring in.
func NewCatchupRepo(t *testing.T) GitRepo {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}

	r := cloneRepo(t, "work")
	r.At(Before, "checkout", "-q", "-b", "main")
	r.Commit(Before, "main.go", "package main\n", "init")
	r.At(Before, "push", "-q", "-u", "origin", "main")
	r.At(Before, "remote", "set-head", "origin", "main")

	r.At(Before, "checkout", "-q", "-b", "FT-1/a")
	r.Commit(Before, "a.go", "package main // a\n", "a")

	r.At(Before, "checkout", "-q", "main")
	r.Commit(During, "b.go", "package main // b\n", "b")
	r.At(During, "push", "-q", "origin", "main")
	r.At(During, "checkout", "-q", "FT-1/a")

	return r
}

func (r GitRepo) Input(guard string) stack.CatchupInput {
	return stack.CatchupInput{
		Dir: r.Dir, Main: r.Dir, Guard: guard,
		Catchup: "rebase", CatchupDefault: true, Stacked: "merge", StackedDefault: true,
	}
}

func (r GitRepo) Resolve(in stack.CatchupInput) stack.CatchupPlan {
	r.T.Helper()

	p, err := stack.ResolveCatchup(r.T.Context(), in)
	if err != nil {
		r.T.Fatal(err)
	}

	return p
}

func (r GitRepo) Sha(ref string) string {
	r.T.Helper()
	return r.At(Before, "rev-parse", ref)
}

// NewStackedRepo is FT-1/a built on FT-2/b, which has a commit of its own,
// with the base tip start records.
func NewStackedRepo(t *testing.T) (GitRepo, string) {
	t.Helper()

	r := NewCatchupRepo(t)
	r.At(During, "checkout", "-q", "-b", "FT-2/b", "main")
	r.Commit(During, "help.go", "package main // help\n", "help")
	r.At(During, "checkout", "-q", "-B", "FT-1/a", "FT-2/b")
	r.Commit(During, "a.go", "package main // a\n", "a")
	r.At(During, "config", branchmeta.After.For("FT-1/a"), "FT-2")
	r.At(During, "config", branchmeta.AfterBranch.For("FT-1/a"), "FT-2/b")

	tip := r.Sha("FT-2/b")
	r.At(During, "config", branchmeta.Tip.For("FT-1/a"), tip)

	return r, tip
}
