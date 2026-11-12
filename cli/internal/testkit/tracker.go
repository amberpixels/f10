package testkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/target"
)

// WriteDriver puts an executable driver in a storage root.
func WriteDriver(t *testing.T, storageRoot string) string {
	t.Helper()

	path := filepath.Join(storageRoot, "driver")
	if err := os.MkdirAll(storageRoot, 0o750); err != nil {
		t.Fatal(err)
	}

	err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700) //nolint:gosec // the driver must run
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// DeclaredTracker is a child target whose project.md declares kind.
func DeclaredTracker(t *testing.T, kind facts.TrackerKind) *target.Target {
	t.Helper()

	tg := ChildTarget(t, "gh")
	tg.Effective.TrackerKind = kind
	tg.Effective.Facts = []facts.Fact{
		{Key: "tracker.kind", Value: kind.String(), Origin: facts.DeclaredFrontmatter("main")},
	}

	return tg
}
