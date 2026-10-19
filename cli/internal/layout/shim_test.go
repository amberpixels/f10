package layout

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shimFixture is a plugin root holding a copy of bin/f10, a cache dir, and
// a stub dir standing in for gh and go on a PATH the test controls.
type shimFixture struct {
	t     *testing.T
	root  string
	cache string
	stubs string
	env   []string
}

const shimVersion = "9.8.7"

func newShimFixture(t *testing.T) *shimFixture {
	t.Helper()

	dir := t.TempDir()
	f := &shimFixture{
		t:     t,
		root:  filepath.Join(dir, "plugin"),
		cache: filepath.Join(dir, "cache"),
		stubs: filepath.Join(dir, "stubs"),
	}

	shim, err := os.ReadFile(filepath.Join(filepath.Dir(scriptPath(t)), "f10"))
	if err != nil {
		t.Fatal(err)
	}

	f.write(filepath.Join(f.root, "bin", "f10"), string(shim), 0o755)
	f.write(
		filepath.Join(f.root, ".claude-plugin", "plugin.json"),
		`{"name": "f10", "version": "`+shimVersion+`"}`,
		0o644,
	)
	f.write(filepath.Join(f.root, "cli", "go.mod"), "module example\n", 0o644)
	must(t, os.MkdirAll(f.stubs, 0o755))

	f.env = []string{
		"HOME=" + dir,
		"F10_BIN_DIR=" + f.cache,
		"PATH=" + f.stubs + ":/usr/bin:/bin",
	}

	return f
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func (f *shimFixture) write(path, body string, mode os.FileMode) {
	f.t.Helper()
	must(f.t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(f.t, os.WriteFile(path, []byte(body), mode))
}

// fakeBinary is what every stub produces: a script naming where it came from.
func fakeBinary(origin string) string {
	return "#!/bin/sh\necho \"" + origin + " $*\"\n"
}

func (f *shimFixture) asset() string {
	arch := runtime.GOARCH
	return "f10_" + shimVersion + "_" + runtime.GOOS + "_" + arch
}

// stubGH answers `gh release download` by writing the asset and a
// checksums file whose sum is sum, or the asset's real one when sum is "".
func (f *shimFixture) stubGH(sum string) {
	body := fakeBinary("release")
	if sum == "" {
		h := sha256.Sum256([]byte(body))
		sum = hex.EncodeToString(h[:])
	}

	f.write(filepath.Join(f.stubs, "gh"), `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in -D) dir="$2"; shift ;; esac
  shift
done
cat >"$dir/`+f.asset()+`" <<'EOF'
`+body+`EOF
printf '%s  %s\n' "`+sum+`" "`+f.asset()+`" >"$dir/checksums.txt"
`, 0o755)
}

func (f *shimFixture) stubGHFailing() {
	f.write(filepath.Join(f.stubs, "gh"), "#!/bin/sh\necho 'release not found' >&2\nexit 1\n", 0o755)
}

// stubGo answers `go build ... -o <file>` with a fake binary, and records
// the ldflags it was given.
func (f *shimFixture) stubGo() {
	f.write(filepath.Join(f.stubs, "go"), `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in -o) out="$2"; shift ;; -ldflags) echo "$2" >"`+f.stubs+`/ldflags"; shift ;; esac
  shift
done
cat >"$out" <<'EOF'
`+fakeBinary("build")+`EOF
chmod +x "$out"
`, 0o755)
}

func (f *shimFixture) run(args ...string) (string, int) {
	f.t.Helper()

	cmd := exec.Command(filepath.Join(f.root, "bin", "f10"), args...)
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return string(out), exitErr.ExitCode()
	} else if err != nil {
		f.t.Fatalf("bin/f10: %v", err)
	}

	return string(out), 0
}

func (f *shimFixture) cached() string { return filepath.Join(f.cache, "f10-"+shimVersion) }

func TestShim(t *testing.T) {
	t.Run("cache hit runs the cached binary", func(t *testing.T) {
		f := newShimFixture(t)
		f.write(f.cached(), fakeBinary("cache"), 0o755)

		out, code := f.run("status", "--json")
		if code != 0 || out != "cache status --json\n" {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("download fills the cache and runs it", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubGH("")

		out, code := f.run("--version")
		if code != 0 || out != "release --version\n" {
			t.Fatalf("got %q (exit %d)", out, code)
		}

		if _, err := os.Stat(f.cached()); err != nil {
			t.Fatalf("not cached: %v", err)
		}
	})

	t.Run("a checksum mismatch fails without building", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubGH(strings.Repeat("0", 64))
		f.stubGo()

		out, code := f.run("--version")
		if code != 1 || !strings.Contains(out, "does not match checksums.txt") {
			t.Fatalf("got %q (exit %d)", out, code)
		}

		if _, err := os.Stat(f.cached()); err == nil {
			t.Fatal("cached a binary that failed its checksum")
		}
	})

	t.Run("no release asset builds from cli/ with the version stamped", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubGHFailing()
		f.stubGo()

		out, code := f.run("--version")
		if code != 0 || out != "build --version\n" {
			t.Fatalf("got %q (exit %d)", out, code)
		}

		ldflags, err := os.ReadFile(filepath.Join(f.stubs, "ldflags"))
		must(t, err)

		if want := "-X main.stampedVersion=" + shimVersion; strings.TrimSpace(string(ldflags)) != want {
			t.Errorf("ldflags = %q, want %q", ldflags, want)
		}

		if _, err := os.Stat(f.cached() + ".built"); err != nil {
			t.Errorf("a checkout build left no marker: %v", err)
		}
	})

	t.Run("neither gh nor go prints the one line", func(t *testing.T) {
		f := newShimFixture(t)
		if _, err := exec.LookPath("/usr/bin/gh"); err == nil {
			t.Skip("gh is installed in /usr/bin")
		}

		out, code := f.run("status")
		if code != 1 || strings.Count(out, "\n") != 1 ||
			!strings.Contains(out, "no binary for "+shimVersion) || !strings.Contains(out, "gh auth login") {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("resolve only does not run the binary", func(t *testing.T) {
		f := newShimFixture(t)
		f.write(f.cached(), fakeBinary("cache"), 0o755)

		out, code := f.run("--shim-resolve")
		if code != 0 || out != "" {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("the hook puts the plugin's bin/ first, once", func(t *testing.T) {
		f := newShimFixture(t)
		f.write(f.cached(), fakeBinary("cache"), 0o755)

		envFile := filepath.Join(t.TempDir(), "env")
		f.env = append(f.env, "CLAUDE_ENV_FILE="+envFile)

		for range 2 {
			if out, code := f.run("--shim-hook"); code != 0 || out != "" {
				t.Fatalf("got %q (exit %d)", out, code)
			}
		}

		data, err := os.ReadFile(envFile)
		must(t, err)

		want := `export PATH="` + filepath.Join(f.root, "bin") + `:$PATH"` + "\n"
		if string(data) != want {
			t.Errorf("env file = %q, want %q", data, want)
		}
	})

	t.Run("the hook says what is missing when neither gh nor go is there", func(t *testing.T) {
		f := newShimFixture(t)
		if _, err := exec.LookPath("/usr/bin/go"); err == nil {
			t.Skip("go is installed in /usr/bin")
		}

		out, code := f.run("--shim-hook")
		if code != 0 || !strings.HasPrefix(out, `{"systemMessage":"f10: no binary for `+shimVersion) {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})
}

// TestShimBuildsTheRealBinary builds cli/ through the shim and checks the
// binary it runs reports the plugin's version.
func TestShimBuildsTheRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the f10 binary")
	}

	shim := filepath.Join(filepath.Dir(scriptPath(t)), "f10")
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(shim), "..", ".claude-plugin", "plugin.json"))
	must(t, err)

	cache := t.TempDir()
	env := append(os.Environ(), "F10_BIN_DIR="+cache)

	build := exec.Command(shim, "--shim-rebuild")
	build.Env = env

	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("--shim-rebuild: %v\n%s", err, out)
	}

	cmd := exec.Command(shim, "--version")
	cmd.Env = env

	out, err := cmd.Output()
	must(t, err)

	fields := strings.Fields(string(out))
	if len(fields) == 0 || !strings.Contains(string(manifest), `"version": "`+fields[len(fields)-1]+`"`) {
		t.Errorf("f10 --version = %q, not the version in plugin.json", out)
	}
}
