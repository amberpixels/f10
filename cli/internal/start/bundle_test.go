package start

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

var refsRider = riderKey("refs/heads/", "GH-2")

func riderKey(root, id string) string {
	patterns := append(stack.TaskRefs(root, id), stack.RiderRefs(root, id)...)

	return "git for-each-ref --format=%(refname) " + strings.Join(patterns, " ")
}

// A bundle starts as one branch named for every member, one worktree and
// one agent named for the lead, with the riders recorded once the branch
// exists and plan and ship prompted with the whole bundle.
func TestStartABundle(t *testing.T) {
	const (
		branch = "GH-1+GH-2/add-dark-mode"
		path   = "/code/repo.GH-1+GH-2-add-dark-mode"
		lead   = "set branch." + branch + "." + string(branchmeta.Task) + " GH-1"
		riders = "set branch." + branch + "." + string(branchmeta.Riders) + " GH-2"
	)

	f := testkit.NewFakes(t)
	f.Script(testkit.RefsLocal, "")
	f.Script(refsRider, "")
	f.Script(testkit.HeadRef, "origin/main")
	f.Script(testkit.Worktrees, testkit.Porcelain(), testkit.Porcelain(branch, path))
	f.Script("git worktree add -b "+branch+" "+path+" origin/main", "")
	f.Script(lead, "")
	f.Script(riders, "")
	f.Script("herdr worktree open --cwd /code/repo --path "+path+" --label "+branch, testkit.Opened)

	agent := agentctl.AgentName("GH-1", "", path)
	f.Script("herdr agent start "+agent+" --kind claude --pane pane:7", "{}")
	f.Script("herdr agent prompt "+agent+" /f10:plan GH-1+GH-2 && /f10:ship GH-1+GH-2 --driven", "{}")

	var out bytes.Buffer

	in := StartInput{
		Main:    "/code/repo",
		Storage: testkit.Storage,
		Task:    testkit.Bundle12,
		Title:   testkit.Titled("Add dark mode", nil),
	}
	if err := Start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	created := testkit.IndexAfter(f.Calls, "git worktree add -b "+branch+" "+path+" origin/main", -1)
	if created < 0 || testkit.IndexAfter(f.Calls, lead, created) < 0 ||
		testkit.IndexAfter(f.Calls, riders, created) < 0 {
		t.Errorf("the bundle was not recorded after the branch existed: %v", f.Calls)
	}

	if !strings.Contains(out.String(), "GH-1+GH-2") {
		t.Errorf("report does not name the bundle:\n%s", out.String())
	}
}

func TestSettleBundle(t *testing.T) {
	t.Run("a rider on a branch of its own", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(refsRider, testkit.Heads("GH-2/own"))

		_, err := settleBundle(t.Context(), "/code/repo", testkit.Bundle12, "")
		if err == nil || !strings.Contains(err.Error(), "GH-2 is already on branch GH-2/own") {
			t.Errorf("err = %v, want the rider's branch named", err)
		}
	})

	t.Run("a rider in another bundle", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Meta.Seed("GH-3+GH-2/x", branchmeta.Riders, "GH-2")
		f.Script(refsRider, "")

		_, err := settleBundle(t.Context(), "/code/repo", testkit.Bundle12, "")
		if err == nil || !strings.Contains(err.Error(), "GH-3+GH-2/x") {
			t.Errorf("err = %v, want the other bundle's branch named", err)
		}
	})

	t.Run("a rider past the lead of a bundle only origin has", func(t *testing.T) {
		f := testkit.NewFakes(t)
		onOrigin := func(p string) bool { return strings.Contains(p, "refs/remotes/origin/") }
		f.Absent = slices.DeleteFunc(f.Absent, onOrigin)
		f.Script(refsRider, "")
		f.Script(riderKey("refs/remotes/origin/", "GH-2"), "refs/remotes/origin/GH-5+GH-2/x\n")

		_, err := settleBundle(t.Context(), "/code/repo", testkit.Bundle12, "")
		if err == nil || !strings.Contains(err.Error(), "GH-2 is already on branch GH-5+GH-2/x") {
			t.Errorf("err = %v, want the remote bundle named", err)
		}
	})

	t.Run("riders changed since start", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Meta.Seed("GH-1+GH-3/x", branchmeta.Riders, "GH-3")
		f.Meta.Seed("GH-1+GH-3/x", branchmeta.Task, "GH-1")

		_, err := settleBundle(t.Context(), "/code/repo", testkit.Bundle12, "GH-1+GH-3/x")
		if err == nil || !strings.Contains(err.Error(), "ships GH-1+GH-3; riders are fixed at start") {
			t.Errorf("err = %v, want the recorded bundle named", err)
		}
	})

	t.Run("the task rides in another bundle", func(t *testing.T) {
		testkit.NewFakes(t)

		_, err := settleBundle(t.Context(), "/code/repo", testkit.Bundle12, "GH-5+GH-1/x")
		if err == nil || !strings.Contains(err.Error(), "GH-1 is already on branch GH-5+GH-1/x, led by GH-5") {
			t.Errorf("err = %v, want the branch's own lead named", err)
		}
	})

	t.Run("the bundle's own branch reopens", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Meta.Seed("GH-1+GH-2/x", branchmeta.Riders, "GH-2")
		f.Meta.Seed("GH-1+GH-2/x", branchmeta.Task, "GH-1")
		f.Meta.Seed("GH-1+GH-2/x", branchmeta.Task, "GH-1")
		f.Meta.Seed("GH-1+GH-2/x", branchmeta.Riders, "GH-2")
		f.Script(refsRider, "")

		if _, err := settleBundle(t.Context(), "/code/repo", testkit.Bundle12, "GH-1+GH-2/x"); err != nil {
			t.Errorf("err = %v reopening the bundle's own branch", err)
		}
	})

	t.Run("the lead alone takes the riders a fetched branch's name spells", func(t *testing.T) {
		f := testkit.NewFakes(t)
		f.Script(refsRider, "")

		got, err := settleBundle(t.Context(), "/code/repo", testkit.Gh1, "GH-1+GH-2/x")
		if err != nil || got.Label() != "GH-1+GH-2" {
			t.Errorf("settleBundle = %s, %v; want the bundle GH-1+GH-2", got.Label(), err)
		}
	})

	t.Run("a task alone is never refused", func(t *testing.T) {
		testkit.NewFakes(t)

		if got, err := settleBundle(t.Context(), "/code/repo", testkit.Gh1, ""); err != nil || got.Bundle != nil {
			t.Errorf("settleBundle = %+v, %v for a single task", got, err)
		}
	})
}

// A parent anywhere in the bundle stops it, named by the member.
func TestStartRefusesABundleWithAParent(t *testing.T) {
	f := testkit.NewFakes(t)
	f.Script(testkit.RefsLocal, "")

	kids := func(_ context.Context, r ref.Ref) ([]stack.ChildRow, error) {
		if r.ID == "GH-2" {
			return []stack.ChildRow{{ID: "GH-5"}}, nil
		}

		return nil, nil
	}

	err := RefuseStartParent(
		t.Context(),
		StartInput{Main: "/code/repo", Storage: testkit.Storage, Task: testkit.Bundle12, Children: kids},
	)
	if err == nil || !strings.Contains(err.Error(), "GH-2 is a parent of GH-5") {
		t.Errorf("err = %v, want GH-2 named as the parent", err)
	}
}
