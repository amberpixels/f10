package testkit

import (
	"path/filepath"
	"testing"
)

func ScopeRepo(t *testing.T, parent, name string) string {
	t.Helper()

	r := InitRepo(t, filepath.Join(parent, name), "main")
	r.Git("commit", "-q", "--allow-empty", "-m", "init")

	return r.Dir
}
