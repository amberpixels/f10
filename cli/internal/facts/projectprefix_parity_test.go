package facts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bin/resolve.sh derives the same prefix for the steps that the binary
// derives for its verbs, in shell. Both are held to one table here, so a
// rule that moves on one side fails until the other follows.
func TestProjectPrefixParity(t *testing.T) {
	script := resolveScript(t)

	for _, name := range []string{
		"f10", "r3", "git-undo", "notion-sdk-go", "runwell", "herdr", "d3rtyjson",
		"obsidian_daily.orbit", "go", "x", "3d-tool", "f10-плагин",
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := os.Mkdir(dir, 0o750); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("bash", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "HOME="+t.TempDir())

			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("resolve.sh failed: %v\n%s", err, out)
			}

			var printed string

			for line := range strings.SplitSeq(string(out), "\n") {
				if v, ok := strings.CutPrefix(line, "task id prefix: "); ok {
					printed, _, _ = strings.Cut(v, " ")
				}
			}

			if want := projectPrefix(name); printed != want {
				t.Errorf("resolve.sh printed prefix %q, projectPrefix(%q) = %q", printed, name, want)
			}
		})
	}
}

func resolveScript(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this source file")
	}

	path, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", "..", "bin", "resolve.sh"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("resolve.sh not found at %s: %v", path, err)
	}

	return path
}
