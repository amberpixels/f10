package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// writeDriver puts an executable driver in a storage root.
func writeDriver(t *testing.T, storageRoot string) string {
	t.Helper()

	path := filepath.Join(storageRoot, "driver")
	if err := os.MkdirAll(storageRoot, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

// A worktree with a .f10/ of its own and no driver finds main's, where it
// finds main's instructions; a worktree replacing main's instructions does not.
func TestDriverFromAWorktree(t *testing.T) {
	main := filepath.Join(t.TempDir(), "main", ".f10")
	wt := filepath.Join(t.TempDir(), "wt", ".f10")
	mainDriver := writeDriver(t, main)

	extends := &target.Target{Layout: &layout.Layout{
		StorageRoot: storage.Root(wt),
		Layers: []layout.Layer{
			{Label: "main", Dir: filepath.Join(main, "instructions")},
			{Label: "worktree", Dir: filepath.Join(wt, "instructions")},
		},
	}}

	if d := extends.Driver(); d == nil || d.Path != mainDriver {
		t.Errorf("driver = %+v, want main's %s", d, mainDriver)
	}

	replaces := &target.Target{Layout: &layout.Layout{
		StorageRoot: storage.Root(wt),
		Layers:      []layout.Layer{{Label: "worktree", Dir: filepath.Join(wt, "instructions")}},
	}}

	if d := replaces.Driver(); d != nil {
		t.Errorf("driver = %+v, want none: the worktree replaces main", d)
	}

	// the worktree's own driver wins over main's
	own := writeDriver(t, wt)
	if d := extends.Driver(); d == nil || d.Path != own {
		t.Errorf("driver = %+v, want the worktree's own %s", d, own)
	}
}

// A declared tracker no host CLI reaches never falls back to host issues.
func TestIssuesRefusesADeclaredNonHostTracker(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".f10")
	gh := &probe.Probes{CLI: probe.Signal{Value: "gh"}}

	notion := &target.Target{
		Layout: &layout.Layout{CheckoutRoot: filepath.Dir(root), StorageRoot: storage.Root(root)},
		Probes: gh,
		Effective: &facts.Effective{
			TrackerKind: facts.TrackerNotion,
			Facts: []facts.Fact{
				{Key: "tracker.kind", Value: "Notion", Origin: facts.DeclaredFrontmatter("main")},
			},
		},
	}

	_, err := hostops.TasksFor(notion).Title(t.Context(), ref.Ref{Number: "7"})
	if err == nil || !strings.Contains(err.Error(), "Notion") ||
		!strings.Contains(err.Error(), filepath.Join(root, "driver")) {
		t.Errorf("Title() = %v, want the Notion refusal naming the driver path", err)
	}

	github := &target.Target{
		Layout: &layout.Layout{CheckoutRoot: filepath.Dir(root), StorageRoot: storage.Root(root)},
		Probes: gh,
		Effective: &facts.Effective{
			TrackerKind: facts.TrackerGitHub,
			Facts:       []facts.Fact{{Key: "tracker.kind", Value: "GitHub Issues", Origin: facts.DeclaredIn("main")}},
		},
	}

	if _, ok := hostops.TasksFor(github).(hostops.HostSource); !ok {
		t.Errorf("a GitHub tracker = %T, want its host", hostops.TasksFor(github))
	}
}

// declaredTracker is a child target whose project.md declares kind.
func declaredTracker(t *testing.T, kind facts.TrackerKind) *target.Target {
	t.Helper()

	tg := childTarget(t, "gh")
	tg.Effective.TrackerKind = kind
	tg.Effective.Facts = []facts.Fact{
		{Key: "tracker.kind", Value: kind.String(), Origin: facts.DeclaredFrontmatter("main")},
	}

	return tg
}

// A driver declining a verb on a non-host tracker is named with that verb,
// never reported as missing.
func TestDeclinedVerbOnNonHostTracker(t *testing.T) {
	f := newFakes(t)
	tg := declaredTracker(t, facts.TrackerNotion)
	path := writeDriver(t, tg.Layout.StorageRoot.Path())

	f.fail("driver list", shell.Result{Code: driver.ExitUnsupported})
	f.fail("driver search index", shell.Result{Code: driver.ExitUnsupported})

	_, listErr := stack.TaskRows(t.Context(), tg, hostops.IssuePage)
	_, searchErr := searchRows(t.Context(), tg, "index")

	for verb, err := range map[string]error{"list": listErr, "search": searchErr} {
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "`"+verb+"`") ||
			strings.Contains(err.Error(), "none was found") {
			t.Errorf("%s declined = %v, want the driver path and the verb, not a missing driver", verb, err)
		}
	}

	if f.called("gh issue list") {
		t.Error("a Notion tracker fell back to host issues")
	}
}

// Every verb falling back to host issues is refused by name on a non-host
// tracker, with what needs it; the title lookup behind the default branch
// name answers for a declined branch.
func TestDeclinedVerbRefusalNamesTheNeed(t *testing.T) {
	tg := declaredTracker(t, facts.TrackerNotion)
	path := writeDriver(t, tg.Layout.StorageRoot.Path())

	verbs := []string{
		driver.VerbRead, driver.VerbURL, driver.VerbIndex, driver.VerbList, driver.VerbSearch, driver.VerbBranch,
	}
	for _, verb := range verbs {
		err := hostops.RefusedSource{Target: tg, Driver: tg.Driver()}.Refuse(verb)
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "`"+verb+"`") ||
			!strings.Contains(err.Error(), hostops.VerbNeeds[verb]) {
			t.Errorf("refuse(%s) = %v, want the driver path, the verb and what needs it", verb, err)
		}
	}

	if !strings.Contains(hostops.VerbNeeds[driver.VerbList], "`f10 task open --print` and the board's next rows") {
		t.Errorf("list need = %q", hostops.VerbNeeds[driver.VerbList])
	}

	if _, err := worktree.TitleLookup(
		tg,
		"7",
	)(
		t.Context(),
	); err == nil ||
		strings.Contains(err.Error(), "none was found") {
		t.Errorf("title lookup = %v, want the declined branch, not a missing driver", err)
	}
}

// A host tracker whose driver declines a verb still falls back to host issues.
func TestDeclinedVerbOnHostTrackerFallsBack(t *testing.T) {
	f := newFakes(t)
	tg := declaredTracker(t, facts.TrackerGitHub)
	writeDriver(t, tg.Layout.StorageRoot.Path())

	f.fail("driver list", shell.Result{Code: driver.ExitUnsupported})
	f.script(
		"gh issue list --json number,title,state,url,labels,body,assignees,createdAt --limit 30",
		`[{"number":7,"title":"host issue","state":"OPEN","url":"https://github.com/o/r/issues/7","labels":[]}]`,
	)

	if rows, err := stack.TaskRows(
		t.Context(),
		tg,
		hostops.IssuePage,
	); err != nil || len(rows) != 1 ||
		rows[0].ID != "FT-7" {
		t.Errorf("fallback = %+v, %v; want the host issue", rows, err)
	}
}
