package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
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

	_, err := notion.issues()
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

	if _, err := github.issues(); err != nil {
		t.Errorf("a GitHub tracker = %v, want its host", err)
	}
}
