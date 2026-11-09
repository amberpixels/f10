package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInside(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "repo.GH-1")

	if err := os.MkdirAll(filepath.Join(wt, "cli"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		dir  string
		want bool
	}{
		{wt, true},
		{filepath.Join(wt, "cli"), true},
		{root, false},
		{filepath.Join(root, "repo.GH-10"), false},
		{filepath.Join(root, "repo"), false},
	}

	for _, c := range cases {
		if got := Inside(c.dir, wt); got != c.want {
			t.Errorf("Inside(%s, %s) = %v, want %v", c.dir, wt, got, c.want)
		}
	}
}

// A path spelled through a symlink is inside the root it really lies in.
func TestInsideResolvesSymlinks(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if !Inside(filepath.Join(target, ".f10"), link) || !Inside(filepath.Join(link, ".f10"), target) {
		t.Error("two spellings of one place disagree")
	}
}
