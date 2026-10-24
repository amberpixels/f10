package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/shell"
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

	extends := &target{lay: &layout.Layout{
		StorageRoot: wt,
		Layers: []layout.Layer{
			{Label: "main", Dir: filepath.Join(main, "instructions")},
			{Label: "worktree", Dir: filepath.Join(wt, "instructions")},
		},
	}}

	if d := extends.driver(); d == nil || d.Path != mainDriver {
		t.Errorf("driver = %+v, want main's %s", d, mainDriver)
	}

	replaces := &target{lay: &layout.Layout{
		StorageRoot: wt,
		Layers:      []layout.Layer{{Label: "worktree", Dir: filepath.Join(wt, "instructions")}},
	}}

	if d := replaces.driver(); d != nil {
		t.Errorf("driver = %+v, want none: the worktree replaces main", d)
	}

	// the worktree's own driver wins over main's
	own := writeDriver(t, wt)
	if d := extends.driver(); d == nil || d.Path != own {
		t.Errorf("driver = %+v, want the worktree's own %s", d, own)
	}
}

// A declared tracker no host CLI reaches never falls back to host issues.
func TestIssuesRefusesADeclaredNonHostTracker(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".f10")
	gh := &probe.Probes{CLI: probe.Signal{Value: "gh"}}

	notion := &target{
		lay: &layout.Layout{StorageRoot: root},
		pb:  gh,
		eff: &facts.Effective{
			TrackerKind: "Notion",
			Facts:       []facts.Fact{{Key: "tracker.kind", Value: "Notion", Origin: "declared (main, frontmatter)"}},
		},
	}

	_, err := notion.issues("")
	if err == nil || !strings.Contains(err.Error(), "Notion") ||
		!strings.Contains(err.Error(), filepath.Join(root, "driver")) {
		t.Errorf("issues() = %v, want the Notion refusal naming the driver path", err)
	}

	github := &target{
		lay: &layout.Layout{StorageRoot: root},
		pb:  gh,
		eff: &facts.Effective{
			TrackerKind: "GitHub Issues",
			Facts:       []facts.Fact{{Key: "tracker.kind", Value: "GitHub Issues", Origin: "declared (main)"}},
		},
	}

	if _, err := github.issues(""); err != nil {
		t.Errorf("a GitHub tracker = %v, want its host", err)
	}
}

// declaredTracker is a child target whose project.md declares kind.
func declaredTracker(t *testing.T, kind string) *target {
	t.Helper()

	tg := childTarget(t, "gh")
	tg.eff.TrackerKind = kind
	tg.eff.Facts = []facts.Fact{{Key: "tracker.kind", Value: kind, Origin: "declared (main, frontmatter)"}}

	return tg
}

// A driver declining a verb on a non-host tracker is named with that verb,
// never reported as missing.
func TestDeclinedVerbOnNonHostTracker(t *testing.T) {
	f := newFakes(t)
	tg := declaredTracker(t, "Notion")
	path := writeDriver(t, tg.lay.StorageRoot)

	f.fail("driver list", shell.Result{Code: driver.ExitUnsupported})
	f.fail("driver search index", shell.Result{Code: driver.ExitUnsupported})

	_, listErr := taskRows(t.Context(), tg, issuePage)
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

// A host tracker whose driver declines a verb still falls back to host issues.
func TestDeclinedVerbOnHostTrackerFallsBack(t *testing.T) {
	f := newFakes(t)
	tg := declaredTracker(t, "GitHub Issues")
	writeDriver(t, tg.lay.StorageRoot)

	f.fail("driver list", shell.Result{Code: driver.ExitUnsupported})
	f.script(
		"gh issue list --json number,title,state,url,labels,body,assignees --limit 30",
		`[{"number":7,"title":"host issue","state":"OPEN","url":"https://github.com/o/r/issues/7","labels":[]}]`,
	)

	if rows, err := taskRows(t.Context(), tg, issuePage); err != nil || len(rows) != 1 || rows[0].ID != "FT-7" {
		t.Errorf("fallback = %+v, %v; want the host issue", rows, err)
	}
}
