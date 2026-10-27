package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/herdr"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

var bundle12 = ref.Ref{ID: "GH-1", Number: "1", Origin: ref.OriginExplicit, Bundle: []string{"GH-1", "GH-2"}}

var refsRider = riderKey("refs/heads/", "GH-2")

// riderKey is the for-each-ref call that looks for every branch a rider is on.
func riderKey(root, id string) string {
	patterns := append(taskRefs(root, id), riderRefs(root, id)...)

	return "git for-each-ref --format=%(refname) " + strings.Join(patterns, " ")
}

// A bundle starts as one branch named for every member, one worktree and
// one agent named for the lead, with the riders recorded once the branch
// exists and plan and ship prompted with the whole bundle.
func TestStartABundle(t *testing.T) {
	const (
		branch = "GH-1+GH-2/add-dark-mode"
		path   = "/code/repo.GH-1+GH-2-add-dark-mode"
		lead   = "git config branch." + branch + "." + cfgTaskID + " GH-1"
		riders = "git config branch." + branch + "." + cfgRiders + " GH-2"
	)

	f := newFakes(t)
	f.script(refsLocal, "")
	f.script(refsRider, "")
	f.script(headRef, "origin/main")
	f.script(worktrees, porcelain(), porcelain(branch, path))
	f.script("git worktree add -b "+branch+" "+path+" origin/main", "")
	f.script(lead, "")
	f.script(riders, "")
	f.script("herdr worktree open --cwd /code/repo --path "+path+" --label "+branch, opened)

	agent := herdr.AgentName("GH-1", "", path)
	f.script("herdr agent start "+agent+" --kind claude --pane pane:7", "{}")
	f.script("herdr agent prompt "+agent+" /f10:plan GH-1+GH-2 && /f10:ship GH-1+GH-2 --driven", "{}")

	var out bytes.Buffer

	in := startInput{main: "/code/repo", task: bundle12, title: titled("Add dark mode", nil)}
	if err := start(t.Context(), &out, in); err != nil {
		t.Fatal(err)
	}

	created := indexAfter(f.calls, "git worktree add -b "+branch+" "+path+" origin/main", -1)
	if created < 0 || indexAfter(f.calls, lead, created) < 0 || indexAfter(f.calls, riders, created) < 0 {
		t.Errorf("the bundle was not recorded after the branch existed: %v", f.calls)
	}

	if !strings.Contains(out.String(), "GH-1+GH-2") {
		t.Errorf("report does not name the bundle:\n%s", out.String())
	}
}

func TestSettleBundle(t *testing.T) {
	t.Run("a rider on a branch of its own", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsRider, heads("GH-2/own"))

		_, err := settleBundle(t.Context(), "/code/repo", bundle12, "")
		if err == nil || !strings.Contains(err.Error(), "GH-2 is already on branch GH-2/own") {
			t.Errorf("err = %v, want the rider's branch named", err)
		}
	})

	t.Run("a rider in another bundle", func(t *testing.T) {
		f := newFakes(t)
		f.script(linkedKeys, "branch.GH-3+GH-2/x."+cfgRiders+" GH-2")
		f.script(refsRider, "")

		_, err := settleBundle(t.Context(), "/code/repo", bundle12, "")
		if err == nil || !strings.Contains(err.Error(), "GH-3+GH-2/x") {
			t.Errorf("err = %v, want the other bundle's branch named", err)
		}
	})

	t.Run("a rider past the lead of a bundle only origin has", func(t *testing.T) {
		f := newFakes(t)
		onOrigin := func(p string) bool { return strings.Contains(p, "refs/remotes/origin/") }
		f.absent = slices.DeleteFunc(f.absent, onOrigin)
		f.script(refsRider, "")
		f.script(riderKey("refs/remotes/origin/", "GH-2"), "refs/remotes/origin/GH-5+GH-2/x\n")

		_, err := settleBundle(t.Context(), "/code/repo", bundle12, "")
		if err == nil || !strings.Contains(err.Error(), "GH-2 is already on branch GH-5+GH-2/x") {
			t.Errorf("err = %v, want the remote bundle named", err)
		}
	})

	t.Run("riders changed since start", func(t *testing.T) {
		f := newFakes(t)
		f.script("git config --get branch.GH-1+GH-3/x."+cfgRiders, "GH-3")
		f.script("git config --get branch.GH-1+GH-3/x."+cfgTaskID, "GH-1")

		_, err := settleBundle(t.Context(), "/code/repo", bundle12, "GH-1+GH-3/x")
		if err == nil || !strings.Contains(err.Error(), "ships GH-1+GH-3; riders are fixed at start") {
			t.Errorf("err = %v, want the recorded bundle named", err)
		}
	})

	t.Run("the task rides in another bundle", func(t *testing.T) {
		newFakes(t)

		_, err := settleBundle(t.Context(), "/code/repo", bundle12, "GH-5+GH-1/x")
		if err == nil || !strings.Contains(err.Error(), "GH-1 is already on branch GH-5+GH-1/x, led by GH-5") {
			t.Errorf("err = %v, want the branch's own lead named", err)
		}
	})

	t.Run("the bundle's own branch reopens", func(t *testing.T) {
		f := newFakes(t)
		f.script("git config --get branch.GH-1+GH-2/x."+cfgRiders, "GH-2")
		f.script("git config --get branch.GH-1+GH-2/x."+cfgTaskID, "GH-1")
		f.script(linkedKeys, "branch.GH-1+GH-2/x."+cfgTaskID+" GH-1\nbranch.GH-1+GH-2/x."+cfgRiders+" GH-2")
		f.script(refsRider, "")

		if _, err := settleBundle(t.Context(), "/code/repo", bundle12, "GH-1+GH-2/x"); err != nil {
			t.Errorf("err = %v reopening the bundle's own branch", err)
		}
	})

	t.Run("the lead alone takes the riders a fetched branch's name spells", func(t *testing.T) {
		f := newFakes(t)
		f.script(refsRider, "")

		got, err := settleBundle(t.Context(), "/code/repo", gh1, "GH-1+GH-2/x")
		if err != nil || got.Label() != "GH-1+GH-2" {
			t.Errorf("settleBundle = %s, %v; want the bundle GH-1+GH-2", got.Label(), err)
		}
	})

	t.Run("a task alone is never refused", func(t *testing.T) {
		newFakes(t)

		if got, err := settleBundle(t.Context(), "/code/repo", gh1, ""); err != nil || got.Bundle != nil {
			t.Errorf("settleBundle = %+v, %v for a single task", got, err)
		}
	})
}

// Any member finds the bundle's branch through the recorded link.
func TestLinkedBranchesFindsAnyMember(t *testing.T) {
	f := newFakes(t)
	f.script(linkedKeys, "branch.GH-1+GH-2/x."+cfgTaskID+" GH-1\nbranch.GH-1+GH-2/x."+cfgRiders+" GH-2 GH-3")

	for _, id := range []string{"GH-1", "GH-3", "gh-2"} {
		if got := linkedBranches(t.Context(), "/code/repo", id); strings.Join(got, ",") != "GH-1+GH-2/x" {
			t.Errorf("linkedBranches(%s) = %v, want [GH-1+GH-2/x]", id, got)
		}
	}

	if got := linkedBranches(t.Context(), "/code/repo", "GH-4"); got != nil {
		t.Errorf("linkedBranches(GH-4) = %v, want none", got)
	}
}

// A parent anywhere in the bundle stops it, named by the member.
func TestStartRefusesABundleWithAParent(t *testing.T) {
	f := newFakes(t)
	f.script(refsLocal, "")

	kids := func(_ context.Context, r ref.Ref) ([]childRow, error) {
		if r.ID == "GH-2" {
			return []childRow{{ID: "GH-5"}}, nil
		}

		return nil, nil
	}

	err := refuseStartParent(t.Context(), startInput{main: "/code/repo", task: bundle12, children: kids})
	if err == nil || !strings.Contains(err.Error(), "GH-2 is a parent of GH-5") {
		t.Errorf("err = %v, want GH-2 named as the parent", err)
	}
}

func TestBundleAfter(t *testing.T) {
	bodies := map[string]string{}

	trimmed := tracker{
		resolve: func(_ context.Context, token string) (ref.Ref, error) {
			return ref.Ref{ID: strings.ToUpper(token)}, nil
		},
		info: func(_ context.Context, r ref.Ref) (taskInfo, error) {
			body, ok := bodies[r.ID]
			if !ok {
				return taskInfo{}, errors.New("no such task")
			}

			return taskInfo{body: body}, nil
		},
	}

	bodies["GH-2"] = "After: GH-7"

	line, err := bundleAfter(t.Context(), trimmed, bundle12, "After: GH-2")
	if err != nil || line != "GH-7" {
		t.Errorf("bundleAfter = %q, %v; want the rider's outside base GH-7", line, err)
	}

	bodies["GH-2"] = "After: GH-8"

	if _, err := bundleAfter(t.Context(), trimmed, bundle12, "After: GH-7"); err == nil ||
		!strings.Contains(err.Error(), "two bases") {
		t.Errorf("err = %v, want two bases refused", err)
	}

	if line, err := bundleAfter(t.Context(), trimmed, gh1, "After: GH-7"); err != nil || line != "GH-7" {
		t.Errorf("a single task's line = %q, %v; want its own body's", line, err)
	}
}

func TestDriveParsesABundle(t *testing.T) {
	a, err := parseDriveArgs(strings.Fields("GH-1+GH-2:opus GH-3"))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(a.tasks, " ") != "GH-1+GH-2 GH-3" || a.models[0] != "opus" {
		t.Errorf("tasks %v models %v, want the bundle whole with its model", a.tasks, a.models)
	}

	in := &driveInput{args: driveArgs{tasks: []string{"GH-1+GH-2", "GH-5"}, span: true}}
	if _, _, err := in.span(t.Context()); err == nil {
		t.Error("a bundle as a range end was taken")
	}
}

// A task after a rider waits for the bundle the rider ships in.
func TestDriveIndexesEveryMember(t *testing.T) {
	tasks := []driveTask{{ref: bundle12}, {ref: ref.Ref{ID: "GH-3"}, base: "GH-2"}}

	index := listIndex(tasks)
	if index["GH-2"] != 0 || index["GH-1"] != 0 || index["GH-3"] != 1 {
		t.Errorf("index = %v, want both members at the bundle's place", index)
	}

	order, err := topoOrder(tasks, index)
	if err != nil || len(order) != 2 || order[0] != 0 {
		t.Errorf("order = %v, %v; want the bundle first", order, err)
	}
}

func TestSkillPromptForABundle(t *testing.T) {
	for skill, want := range map[string]string{
		"plan":   "/f10:plan GH-1+GH-2 --driven",
		"ship":   "/f10:ship GH-1+GH-2 --driven",
		"review": "/f10:review GH-1 --driven",
		"finish": "/f10:finish GH-1 --driven",
	} {
		if got := skillPrompt(skill, "GH-1+GH-2", nil, shipFlow{}); got != want {
			t.Errorf("skillPrompt(%s) = %q, want %q", skill, got, want)
		}
	}
}

// A bundle's merge waits for the riders' answer, before anything changes.
func TestFinishAsksWhichRidersClose(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.task = bundle12

	_, err := fx.run(t)

	var re ridersError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "riders GH-2") {
		t.Fatalf("err = %v, want the riders asked", err)
	}

	if fx.f.called("gh pr merge") || fx.f.called("gh pr edit") {
		t.Errorf("something changed before the answer: %v", fx.f.calls)
	}
}

// A rider left open loses its closing keyword before the merge; one the PR
// delivers keeps it, and the report names it closed.
func TestFinishKeepsARiderOpen(t *testing.T) {
	for name, tc := range map[string]struct {
		close  []string
		edited bool
		want   string
	}{
		"left open": {close: []string{}, edited: true, want: "left open: GH-2"},
		"closed":    {close: []string{"GH-2"}, want: "GH-2"},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFinishFixture(t)
			fx.openPR()
			fx.in.task, fx.in.close = bundle12, tc.close
			fx.f.script("gh pr view GH-1 --json body", `{"body":"Closes #1\nCloses #2"}`)
			fx.f.absent = append(fx.f.absent, "gh pr edit GH-1 --body-file ")

			out, err := fx.run(t)
			if err != nil {
				t.Fatal(err)
			}

			if fx.f.called("gh pr edit") != tc.edited {
				t.Errorf("body edited = %v, want %v: %v", !tc.edited, tc.edited, fx.f.calls)
			}

			if edit := indexAfter(fx.f.calls, "gh pr edit", -1); tc.edited &&
				indexAfter(fx.f.calls, ghMerge, -1) < edit {
				t.Errorf("the body was edited after the merge: %v", fx.f.calls)
			}

			if !strings.Contains(out, tc.want) {
				t.Errorf("report missing %q:\n%s", tc.want, out)
			}
		})
	}
}

// After a merge that closed every member, a rider left open is reopened.
func TestFinishReopensAKeptRiderAfterTheMerge(t *testing.T) {
	fx := newFinishFixture(t)
	fx.f.script(ghView, prMerged)
	fx.f.script(lsRem, "")
	fx.f.script("gh issue reopen 2", "")
	fx.in.task, fx.in.close = bundle12, []string{}

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if !fx.f.called("gh issue reopen 2") || !strings.Contains(out, "reopened GH-2") {
		t.Errorf("GH-2 was not reopened: %v\n%s", fx.f.calls, out)
	}
}

func TestKeepOpen(t *testing.T) {
	body := "Closes #12\nfixes: #12\nResolved https://github.com/a/b/issues/12\nCloses #123\nSee #12\nCloses #3"
	want := "Part of #12\nPart of #12\nPart of https://github.com/a/b/issues/12\nCloses #123\nSee #12\nCloses #3"

	if got := keepOpen(body, "12"); got != want {
		t.Errorf("keepOpen =\n%s\nwant\n%s", got, want)
	}
}

func TestCloseChoice(t *testing.T) {
	task := ref.Ref{ID: "GH-1", Bundle: []string{"GH-1", "GH-2", "GH-3"}}
	resolve := func(tok string) (ref.Ref, error) {
		return ref.Ref{ID: "GH-" + strings.TrimPrefix(strings.ToUpper(tok), "GH-")}, nil
	}

	for value, want := range map[string]string{"all": "GH-2 GH-3", "none": "", "3": "GH-3", "gh-2, 3": "GH-2 GH-3"} {
		got, err := closeChoice(task, value, resolve)
		if err != nil || strings.Join(got, " ") != want || got == nil {
			t.Errorf("closeChoice(%q) = %v, %v; want [%s]", value, got, err, want)
		}
	}

	if _, err := closeChoice(task, "GH-1", resolve); err == nil {
		t.Error("the lead was taken as a rider")
	}
}

// A bundle's events carry its lead; a rider finds them through the branch.
func TestLedgerMatchesABundleMember(t *testing.T) {
	e := ledger.Event{Task: "GH-1", Branch: "GH-1+GH-2/slug"}

	for id, want := range map[string]bool{"GH-1": true, "gh-2": true, "GH-3": false} {
		if got := onTask(e, id); got != want {
			t.Errorf("onTask(%s) = %v, want %v", id, got, want)
		}
	}

	if got := eventTask(e); got != "GH-1+GH-2" {
		t.Errorf("eventTask = %q, want the bundle", got)
	}

	if got := eventTask(ledger.Event{Task: "GH-1", Branch: "GH-1/slug"}); got != "GH-1" {
		t.Errorf("eventTask on a single branch = %q, want GH-1", got)
	}
}

// A tracker that is not the host closes nothing on merge: no closed row, no
// body edit, and the notes name what to close there.
func TestFinishWithATrackerDriverClosesNothing(t *testing.T) {
	fx := newFinishFixture(t)
	fx.openPR()
	fx.in.task, fx.in.close, fx.in.driver = bundle12, []string{"GH-2"}, fakeDriver(t)
	fx.f.fail("driver branch 1", shell.Result{Code: 3})

	out, err := fx.run(t)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, "closed:") || fx.f.called("gh pr view GH-1 --json body") {
		t.Errorf("a driver tracker reported or edited a close:\n%s\n%v", out, fx.f.calls)
	}

	if !strings.Contains(out, "close GH-2 there") {
		t.Errorf("report does not name the rider to close by hand:\n%s", out)
	}
}

// A rerun after a failed merge closes a rider an earlier run kept open.
func TestCloseAgain(t *testing.T) {
	body := keepOpen("Closes #1\nCloses #2", "2")
	if got := closeAgain(body, "2"); got != "Closes #1\nCloses #2" {
		t.Errorf("closeAgain = %q, want the keyword back", got)
	}

	if got := closeAgain("Part of #22", "2"); got != "Part of #22" {
		t.Errorf("closeAgain touched another issue: %q", got)
	}
}
