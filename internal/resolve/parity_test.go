package resolve

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The parity suite pins this package to bin/resolve.sh, the semantics
// oracle. Every fixture runs both and compares the script's header lines
// with the Go resolution, so a semantic change to either side fails here
// until both move.

type scriptFacts struct {
	checkoutRoot string
	source       string
	storageRoot  string
}

func TestParity(t *testing.T) {
	script := scriptPath(t)

	cases := []struct {
		name       string
		setup      func(t *testing.T, home string) string // returns the dir to run from
		wantNested bool
	}{
		{
			name: "plain dir, no git, no instructions",
			setup: func(t *testing.T, _ string) string {
				t.Helper()
				return canon(t.TempDir())
			},
		},
		{
			name: "repo with instructions, from the root",
			setup: func(t *testing.T, _ string) string {
				t.Helper()
				repo := mkRepo(t)
				writeInstr(t, repo, map[string]string{"project.md": "- **Project** - a repo\n"})
				return repo
			},
		},
		{
			name: "repo with instructions, from a subdirectory",
			setup: func(t *testing.T, _ string) string {
				t.Helper()
				repo := mkRepo(t)
				writeInstr(t, repo, map[string]string{"project.md": "- **Project** - a repo\n"})
				sub := filepath.Join(repo, "sub", "deep")
				mkdir(t, sub)
				return sub
			},
		},
		{
			name: "repo without instructions",
			setup: func(t *testing.T, _ string) string {
				t.Helper()
				return mkRepo(t)
			},
		},
		{
			name: "out-of-tree storage",
			setup: func(t *testing.T, home string) string {
				t.Helper()
				repo := mkRepo(t)
				oot := filepath.Join(home, ".f10", filepath.Base(repo), "instructions")
				mkdir(t, oot)
				writeFile(t, filepath.Join(oot, "project.md"), "- **Project** - out of tree\n")
				return repo
			},
		},
		{
			name: "worktree extending main",
			setup: func(t *testing.T, _ string) string {
				t.Helper()
				repo := mkRepo(t)
				writeInstr(t, repo, map[string]string{"project.md": "- **Project** - main\n"})
				wt := addWorktree(t, repo)
				writeInstr(t, wt, map[string]string{"project.md": "- **Verify** - go test ./...\n"})
				return wt
			},
		},
		{
			name: "worktree replacing main",
			setup: func(t *testing.T, _ string) string {
				t.Helper()
				repo := mkRepo(t)
				writeInstr(t, repo, map[string]string{"project.md": "- **Project** - main\n"})
				wt := addWorktree(t, repo)
				writeInstr(t, wt, map[string]string{
					"project.md": "- **Layering** - replaces main\n- **Project** - Go rewrite\n",
				})
				return wt
			},
		},
		{
			name: "worktree without instructions falls back to main",
			setup: func(t *testing.T, _ string) string {
				t.Helper()
				repo := mkRepo(t)
				writeInstr(t, repo, map[string]string{"project.md": "- **Project** - main\n"})
				return addWorktree(t, repo)
			},
		},
		{
			name: "nested instructions below the root are ignored",
			setup: func(t *testing.T, _ string) string {
				t.Helper()
				repo := mkRepo(t)
				writeInstr(t, repo, map[string]string{"project.md": "- **Project** - main\n"})
				sub := filepath.Join(repo, "svc")
				writeInstr(t, sub, map[string]string{"project.md": "- **Project** - nested\n"})
				return sub
			},
			wantNested: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			runDir := tc.setup(t, home)
			res := Resolve(t.Context(), runDir)
			want := runScript(t, script, runDir, home)

			if res.CheckoutRoot != want.checkoutRoot {
				t.Errorf("checkout root:\n  go:     %q\n  script: %q", res.CheckoutRoot, want.checkoutRoot)
			}

			if res.Source != want.source {
				t.Errorf("source:\n  go:     %q\n  script: %q", res.Source, want.source)
			}

			if res.StorageRoot != want.storageRoot {
				t.Errorf("storage root:\n  go:     %q\n  script: %q", res.StorageRoot, want.storageRoot)
			}

			if got := res.NestedIgnored != ""; got != tc.wantNested {
				t.Errorf("nested ignored: got %v (%q), want %v", got, res.NestedIgnored, tc.wantNested)
			}
		})
	}
}

// scriptPath locates bin/resolve.sh relative to this source file.
func scriptPath(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this source file")
	}

	path, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", "bin", "resolve.sh"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("resolve.sh not found at %s: %v", path, err)
	}

	return path
}

// runScript runs bin/resolve.sh in dir with an overridden HOME and parses
// the three header facts this suite compares.
func runScript(t *testing.T, script, dir, home string) scriptFacts {
	t.Helper()

	cmd := exec.Command("bash", script)
	cmd.Dir = dir

	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}

	cmd.Env = append(cmd.Env, "HOME="+home)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve.sh failed: %v\n%s", err, out)
	}

	var facts scriptFacts

	for line := range strings.SplitSeq(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "checkout root: "); ok {
			facts.checkoutRoot = v
		}

		if v, ok := strings.CutPrefix(line, "instructions source: "); ok {
			facts.source = v
		}

		if v, ok := strings.CutPrefix(line, "storage root: "); ok {
			facts.storageRoot, _, _ = strings.Cut(v, "  (plans")
		}
	}

	return facts
}

func mkRepo(t *testing.T) string {
	t.Helper()

	dir := canon(t.TempDir())
	run(t, dir, "git", "init", "-q")
	run(t, dir,
		"git", "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
		"commit", "--allow-empty", "-q", "-m", "init")

	return dir
}

func addWorktree(t *testing.T, mainRoot string) string {
	t.Helper()

	wt := filepath.Join(t.TempDir(), "wt")
	run(t, mainRoot, "git", "worktree", "add", "-q", wt, "-b", "wt")

	return canon(wt)
}

func writeInstr(t *testing.T, root string, files map[string]string) {
	t.Helper()

	dir := filepath.Join(root, ".f10", "instructions")
	mkdir(t, dir)

	for name, body := range files {
		writeFile(t, filepath.Join(dir, name), body)
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}
