package layout

import (
	"path/filepath"
	"testing"
)

func TestStorageRoot(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(t *testing.T, repo, wt string)
		wantOwn    bool // the worktree's own .f10, else main's
		wantLegacy bool
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
			wantOwn:    true,
			wantLegacy: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			repo := mkRepo(t)
			wt := addWorktree(t, repo)
			tc.setup(t, repo, wt)

			lay := Locate(t.Context(), wt)

			want := filepath.Join(repo, ".f10")
			if tc.wantOwn {
				want = filepath.Join(wt, ".f10")
			}

			if lay.StorageRoot != want || lay.LegacyStorage != tc.wantLegacy {
				t.Errorf("Locate = %q, legacy %v; want %q, legacy %v",
					lay.StorageRoot, lay.LegacyStorage, want, tc.wantLegacy)
			}
		})
	}
}
