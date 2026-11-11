package storage

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRootPaths(t *testing.T) {
	t.Parallel()

	for _, root := range []Root{InRepo("/code/repo"), OutOfTree("/home/me", "repo")} {
		base := string(root)
		cases := map[string]string{
			root.Path():           base,
			root.Plans():          filepath.Join(base, "plans"),
			root.Plan("GH-1"):     filepath.Join(base, "plans", "GH-1.md"),
			root.Archive():        filepath.Join(base, "plans", "archive"),
			root.Reviews("GH-1"):  filepath.Join(base, "reviews", "GH-1"),
			root.Reviews(""):      filepath.Join(base, "reviews"),
			root.Verdicts("GH-1"): filepath.Join(base, "verdicts", "GH-1"),
			root.Spikes():         filepath.Join(base, "spikes"),
			root.Handoffs():       filepath.Join(base, "handoffs"),
			root.Lore():           filepath.Join(base, "lore"),
			root.Images("GH-1"):   filepath.Join(base, "images", "GH-1"),
			root.Demo("GH-1"):     filepath.Join(base, "demo", "GH-1"),
			root.Instructions():   filepath.Join(base, "instructions"),
			root.Driver():         filepath.Join(base, "driver"),
		}

		for got, want := range cases {
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		}
	}

	if got := InRepo("/code/repo"); got != "/code/repo/.f10" {
		t.Errorf("InRepo = %q", got)
	}

	if got := OutOfTree("/home/me", "repo"); got != "/home/me/.f10/repo" {
		t.Errorf("OutOfTree = %q", got)
	}
}

// Every dir a method names for a task file is granted to the agent, so a
// new one cannot land in one place and not the other.
func TestTaskFileDirsCoverMethods(t *testing.T) {
	t.Parallel()

	root := InRepo("/code/repo")
	granted := root.TaskFileDirs()

	for _, dir := range []string{
		root.Plans(), filepath.Dir(root.Reviews("x")), filepath.Dir(root.Verdicts("x")), root.Spikes(),
		root.Handoffs(), root.Lore(), filepath.Dir(root.Images("x")), filepath.Dir(root.Demo("x")),
	} {
		if !slices.Contains(granted, dir) {
			t.Errorf("%s is not granted to the agent", dir)
		}
	}

	if len(granted) != 8 {
		t.Errorf("TaskFileDirs = %v, want the eight task-file dirs", granted)
	}

	for _, d := range granted {
		if d == root.Instructions() || d == root.Driver() {
			t.Errorf("%s is granted to the agent", d)
		}
	}
}

func TestOutside(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		root     Root
		worktree string
		want     bool
	}{
		{"worktree beside main", InRepo("/code/repo"), "/code/repo.GH-1", true},
		{"main itself", InRepo("/code/repo"), "/code/repo", false},
		{"out of tree", OutOfTree("/home/me", "repo"), "/code/repo.GH-1", true},
	}

	for _, c := range cases {
		if got := c.root.Outside(c.worktree); got != c.want {
			t.Errorf("%s: Outside = %v, want %v", c.name, got, c.want)
		}
	}
}

// A worktree reached through a symlink holds the root it really contains.
func TestOutsideResolvesSymlinks(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if InRepo(target).Outside(link) {
		t.Error("the root under the symlinked worktree counts as outside")
	}
}

func TestEmptyRootPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("an empty root resolved a path")
		}
	}()

	_ = Root("").Plans()
}

// The ledger, the daemon and out-of-tree roots read UserDir, the user's
// files UserFilesDir; both honour the override.
func TestUserDirHonoursTheOverride(t *testing.T) {
	t.Setenv(UserDirEnv, "/elsewhere/f10")

	if got := UserDir("/home/me"); got != "/elsewhere/f10" {
		t.Errorf("UserDir = %s", got)
	}

	if got, err := UserFilesDir(); err != nil || got != "/elsewhere/f10" {
		t.Errorf("UserFilesDir = %s, %v", got, err)
	}
}
