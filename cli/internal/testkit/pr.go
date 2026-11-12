package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// PrRepo is a clone of a bare origin, so origin/HEAD and pushed branches
// exist; origin is a path, so no host CLI applies and every read is git's.
func PrRepo(t *testing.T) string {
	t.Helper()

	dir := cloneRepo(t, "repo").Dir
	CommitFile(t, dir, "main.txt")
	Git(t, dir, "push", "-q", "origin", "main")
	Git(t, dir, "remote", "set-head", "origin", "-a")

	return dir
}

func CommitFile(t *testing.T, dir, name string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	Git(t, dir, "add", name)
	Git(t, dir, "commit", "-q", "-m", name)
}
