package branchmeta_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/branchmeta/branchmetatest"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

func gitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if _, err := gitx.Exec(t.Context(), dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}

	return dir
}

// writes is one scenario both backends run, so the answers can be compared.
func writes(t *testing.T, ctx context.Context, dir string) {
	t.Helper()

	for _, w := range []struct {
		branch string
		key    branchmeta.Key
		value  string
	}{
		{"a.b/c.d", branchmeta.Task, "GH-1"},
		{"a.b/c.d", branchmeta.Riders, "GH-2 GH-3"},
		{"a.b/c.d", branchmeta.After, "GH-7"},
		{"a.b/c.d", branchmeta.AfterBranch, "GH-7/base"},
		{"a.b/c.d", branchmeta.Finish, "running: merging #204"},
		{"a.b/c.d", branchmeta.Chain, "/f10:plan /f10:ship"},
		{"GH-8", branchmeta.AfterBranch, "GH-7/base"},
		{"GH-8", branchmeta.Landed, "GH-7"},
		{"GH-9", branchmeta.Watch, "false"},
	} {
		if err := branchmeta.Set(ctx, dir, w.branch, w.key, w.value); err != nil {
			t.Fatal(err)
		}
	}

	if err := branchmeta.Unset(ctx, dir, "GH-8", branchmeta.Landed); err != nil {
		t.Fatal(err)
	}
}

func TestGitRoundTrip(t *testing.T) {
	t.Parallel()

	ctx, dir := t.Context(), gitRepo(t)
	writes(t, ctx, dir)

	// a key this binary does not know is skipped, not an error
	if _, err := gitx.Exec(ctx, dir, "config", "branch.GH-9.f10-later", "x"); err != nil {
		t.Fatal(err)
	}

	got, err := branchmeta.Get(ctx, dir, "a.b/c.d", branchmeta.AfterBranch)
	if err != nil || got != "GH-7/base" {
		t.Errorf("Get on a dotted branch = %q, %v", got, err)
	}

	if got, err := branchmeta.Get(ctx, dir, "GH-8", branchmeta.Landed); err != nil || got != "" {
		t.Errorf("an unset key = %q, %v; want absent", got, err)
	}

	if err := branchmeta.Unset(ctx, dir, "GH-8", branchmeta.Landed); err == nil {
		t.Error("unsetting an absent key was no error")
	}

	if err := branchmeta.UnsetIfSet(ctx, dir, "GH-8", branchmeta.Landed); err != nil {
		t.Errorf("UnsetIfSet on an absent key = %v", err)
	}

	rec, err := branchmeta.Load(ctx, dir, "a.b/c.d")
	if err != nil {
		t.Fatal(err)
	}

	want := branchmeta.Record{
		Branch: "a.b/c.d", Task: "GH-1", Riders: []string{"GH-2", "GH-3"}, After: "GH-7", AfterBranch: "GH-7/base",
		Finish: &branchmeta.DriveFinish{State: branchmeta.FinishRunning, Action: "merging #204"},
		Chain:  []string{"/f10:plan", "/f10:ship"},
	}
	if !reflect.DeepEqual(rec, want) {
		t.Errorf("Load = %+v\nwant %+v", rec, want)
	}

	all, err := branchmeta.LoadAll(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	if deps := branchmeta.Dependents(all, "GH-7/base"); !reflect.DeepEqual(deps, []string{"a.b/c.d", "GH-8"}) {
		t.Errorf("Dependents = %v", deps)
	}

	if none, err := branchmeta.Load(
		ctx,
		dir,
		"nowhere",
	); err != nil ||
		!reflect.DeepEqual(none, branchmeta.Record{Branch: "nowhere"}) {
		t.Errorf("a branch with no record = %+v, %v", none, err)
	}
}

// Memory answers what git answers for the same writes.
func TestMemoryMatchesGit(t *testing.T) {
	t.Parallel()

	ctx, dir := t.Context(), gitRepo(t)
	writes(t, ctx, dir)

	fromGit, err := branchmeta.LoadAll(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	m := branchmetatest.Use(t)
	writes(t, ctx, "/anywhere")

	fromMemory, err := branchmeta.LoadAll(ctx, "/anywhere")
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(fromGit, fromMemory) {
		t.Errorf("memory = %+v\ngit    = %+v", fromMemory, fromGit)
	}

	if !m.Wrote("GH-9", branchmeta.Watch, "false") || !m.Unsetted("GH-8", branchmeta.Landed) {
		t.Errorf("writes not recorded: %+v", m.Writes())
	}

	if err := branchmeta.Unset(ctx, "/anywhere", "GH-8", branchmeta.Landed); err == nil {
		t.Error("memory unset an absent key without an error, unlike git")
	}
}

func TestSplit(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]struct {
		branch string
		key    branchmeta.Key
		ok     bool
	}{
		"branch.GH-1.f10-after-branch": {"GH-1", branchmeta.AfterBranch, true},
		"branch.GH-1.f10-after":        {"GH-1", branchmeta.After, true},
		"branch.v1.2/x.f10-tasks":      {"v1.2/x", branchmeta.Riders, true},
		"branch.GH-1.f10-task":         {"GH-1", branchmeta.Task, true},
		"branch.GH-1.merge":            {"", "", false},
		"branch..f10-task":             {"", "", false},
		"remote.origin.f10-task":       {"", "", false},
	} {
		branch, key, ok := branchmeta.Split(name)
		if branch != want.branch || key != want.key || ok != want.ok {
			t.Errorf("Split(%q) = %q, %q, %v; want %+v", name, branch, key, ok, want)
		}
	}
}
