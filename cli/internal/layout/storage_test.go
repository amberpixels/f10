package layout

import (
	"path/filepath"
	"testing"
)

// Main's .f10 is every worktree's storage root, even one whose own .f10
// holds task files.
func TestStorageRoot(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, repo, wt string)
	}{
		{name: "worktree with no .f10", setup: func(*testing.T, string, string) {}},
		{
			name: "worktree with an instructions layer only",
			setup: func(t *testing.T, repo, wt string) {
				t.Helper()
				writeInstr(t, repo, map[string]string{"project.md": "- **Project** - main\n"})
				writeInstr(t, wt, map[string]string{"project.md": "- **Verify** - go test ./...\n"})
			},
		},
		{
			name: "worktree with its own verdicts",
			setup: func(t *testing.T, _, wt string) {
				t.Helper()
				mkdir(t, filepath.Join(wt, ".f10", "verdicts"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			repo := mkRepo(t)
			wt := addWorktree(t, repo)
			tc.setup(t, repo, wt)

			if lay := Locate(t.Context(), wt); lay.StorageRoot.Path() != filepath.Join(repo, ".f10") {
				t.Errorf("Locate = %q, want main's .f10", lay.StorageRoot)
			}
		})
	}
}
