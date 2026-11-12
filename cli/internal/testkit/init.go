package testkit

import (
	"path/filepath"
	"testing"
)

// IsolateEnv keeps the probes off the developer's own machine: both the
// gh/glab config lookup and the out-of-tree storage root hang off HOME.
func IsolateEnv(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	return home
}

// MkRepo is a checkout named demo: the name derives the task id prefix
// init writes, and a temp dir's own basename derives none.
func MkRepo(t *testing.T) string {
	t.Helper()

	r := InitRepo(t, Canonical(t, t.TempDir())+"/demo", "master")
	r.Git("remote", "add", "origin", "git@github.com:amberpixels/demo.git")

	return r.Dir
}

// Canonical matches the layout package, which reports physical paths - a temp dir
// on macOS is reached through a symlink.
func Canonical(t *testing.T, path string) string {
	t.Helper()

	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("canonicalizing %s: %v", path, err)
	}

	return p
}
