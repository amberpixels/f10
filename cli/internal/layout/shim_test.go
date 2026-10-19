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
	"time"
)

// shimFixture is a plugin root holding a copy of bin/f10, a cache dir, and
// a stub dir standing in for curl and go on a PATH the test controls.
type shimFixture struct {
	t     *testing.T
	root  string
	cache string
	stubs string
	env   []string
}

const shimVersion = "9.8.7"

// shimTools are the commands the shim and the stubs run. The fixture's PATH
// reaches them through symlinks and nothing else, so a real curl or go on the
// machine never takes part.
var shimTools = []string{
	"awk", "bash", "cat", "chmod", "cksum", "dirname", "find", "grep", "head", "mkdir",
	"mktemp", "mv", "nohup", "perl", "rm", "sed", "sh", "sha256sum", "shasum", "touch", "tr", "uname",
}

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

	tools := filepath.Join(dir, "tools")
	must(t, os.MkdirAll(tools, 0o755))

	for _, name := range shimTools {
		if path, err := exec.LookPath(name); err == nil {
			must(t, os.Symlink(path, filepath.Join(tools, name)))
		}
	}

	f.env = []string{
		"HOME=" + dir,
		"F10_BIN_DIR=" + f.cache,
		"PATH=" + f.stubs + ":" + tools,
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
	return "f10_" + shimVersion + "_" + runtime.GOOS + "_" + runtime.GOARCH
}

// stubCurl answers `curl -o <file> -w ... <url>` like a public release: the
// asset, or a checksums file whose sum is sum (the asset's real one when sum
// is ""), and HTTP 200. Each url it was asked for goes to the urls file.
func (f *shimFixture) stubCurl(sum string) { f.stubCurlChecksums(sum, "200") }

// stubCurlChecksums is stubCurl with checksums.txt answering HTTP status.
func (f *shimFixture) stubCurlChecksums(sum, status string) {
	body := fakeBinary("release")
	if sum == "" {
		h := sha256.Sum256([]byte(body))
		sum = hex.EncodeToString(h[:])
	}

	f.write(filepath.Join(f.stubs, "curl"), `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in
  -o) out="$2"; shift ;;
  -w | --connect-timeout | --speed-limit | --speed-time) shift ;;
  -*) ;;
  *) url="$1" ;;
  esac
  shift
done
echo "$url" >>"`+f.urls()+`"
case "$url" in
*/checksums.txt)
  printf '%s  %s\n' "`+sum+`" "`+f.asset()+`" >"$out"
  printf `+status+`
  ;;
*)
  cat >"$out" <<'EOF'
`+body+`EOF
  printf 200
  ;;
esac
`, 0o755)
}

// stubCurlNotFound answers every url the way GitHub answers a missing asset.
func (f *shimFixture) stubCurlNotFound() {
	f.write(filepath.Join(f.stubs, "curl"), `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in -o) out="$2"; shift ;; esac
  shift
done
echo 'Not Found' >"$out"
printf 404
`, 0o755)
}

// stubCurlOffline fails the way curl does when DNS cannot resolve github.com.
func (f *shimFixture) stubCurlOffline() {
	f.write(filepath.Join(f.stubs, "curl"),
		"#!/bin/sh\necho 'curl: (6) Could not resolve host: github.com' >&2\nprintf 000\nexit 6\n", 0o755)
}

func (f *shimFixture) urls() string { return filepath.Join(f.stubs, "urls") }

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

func (f *shimFixture) release() string { return filepath.Join(f.cache, "f10-"+shimVersion) }

// build is this checkout's build slot, f10-<version>-<root key>, or "".
func (f *shimFixture) build() string {
	f.t.Helper()

	matches, err := filepath.Glob(filepath.Join(f.cache, "f10-"+shimVersion+"-*"))
	must(f.t, err)

	for _, m := range matches {
		if !strings.HasSuffix(m, ".built") {
			return m
		}
	}

	return ""
}

// eventually polls cond for a background job the hook started.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return
		}
	}

	t.Fatalf("timed out waiting for %s", what)
}

func TestShim(t *testing.T) {
	t.Run("cache hit runs the cached binary", func(t *testing.T) {
		f := newShimFixture(t)
		f.write(f.release(), fakeBinary("cache"), 0o755)

		out, code := f.run("status", "--json")
		if code != 0 || out != "cache status --json\n" {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("download fills the cache and runs it", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubCurl("")

		out, code := f.run("--version")
		if code != 0 || out != "release --version\n" {
			t.Fatalf("got %q (exit %d)", out, code)
		}

		if _, err := os.Stat(f.release()); err != nil {
			t.Fatalf("not cached: %v", err)
		}

		urls, err := os.ReadFile(f.urls())
		must(t, err)

		base := "https://github.com/amberpixels/f10/releases/download/cli/v" + shimVersion + "/"
		if want := base + f.asset() + "\n" + base + "checksums.txt\n"; string(urls) != want {
			t.Errorf("fetched %q, want %q", urls, want)
		}
	})

	t.Run("a checksum mismatch fails without building", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubCurl(strings.Repeat("0", 64))
		f.stubGo()

		out, code := f.run("--version")
		if code != 1 || !strings.Contains(out, "does not match checksums.txt") {
			t.Fatalf("got %q (exit %d)", out, code)
		}

		if _, err := os.Stat(f.release()); err == nil {
			t.Fatal("cached a binary that failed its checksum")
		}
	})

	t.Run("no release asset builds from cli/ with the version stamped", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubCurlNotFound()
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

		if f.build() == "" {
			t.Fatal("the build is not in this checkout's slot")
		}

		if _, err := os.Stat(f.build() + ".built"); err != nil {
			t.Errorf("a checkout build left no marker: %v", err)
		}
	})

	t.Run("this checkout's build wins over the release binary", func(t *testing.T) {
		f := newShimFixture(t)
		f.write(f.release(), fakeBinary("release"), 0o755)
		f.stubGo()

		if out, code := f.run("--shim-rebuild"); code != 0 {
			t.Fatalf("--shim-rebuild: %q (exit %d)", out, code)
		}

		if out, _ := f.run("x"); out != "build x\n" {
			t.Fatalf("got %q", out)
		}
	})

	t.Run("neither curl nor go prints the one line", func(t *testing.T) {
		f := newShimFixture(t)

		out, code := f.run("status")
		if code != 1 || strings.Count(out, "\n") != 1 ||
			!strings.Contains(out, "no binary for "+shimVersion) || !strings.Contains(out, "curl is not installed") ||
			!strings.Contains(out, "install Go") {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("a missing release is named by its asset", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubCurlNotFound()

		out, code := f.run("status")
		if code != 1 || !strings.Contains(out, "no public release asset "+f.asset()+" for cli/v"+shimVersion) {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("a release missing checksums.txt names it and still builds", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubCurlChecksums("", "404")

		out, code := f.run("status")
		if code != 1 || !strings.Contains(out, "no public release asset checksums.txt for cli/v"+shimVersion) {
			t.Fatalf("got %q (exit %d)", out, code)
		}

		f.stubGo()

		if out, code := f.run("--version"); code != 0 || out != "build --version\n" {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("an HTTP error names its status", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubCurlChecksums("", "503")

		out, code := f.run("status")
		if code != 1 || !strings.Contains(out, "checksums.txt answered HTTP 503") {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("an unreachable network is named as such", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubCurlOffline()

		out, code := f.run("status")
		if code != 1 || !strings.Contains(out, "cannot reach github.com, and go is not installed") {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("resolve only does not run the binary", func(t *testing.T) {
		f := newShimFixture(t)
		f.write(f.release(), fakeBinary("cache"), 0o755)

		out, code := f.run("--shim-resolve")
		if code != 0 || out != "" {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("cached only fails fast on an empty cache", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubCurl("")
		f.env = append(f.env, "F10_SHIM_CACHED_ONLY=1")

		out, code := f.run("status")
		if code != 1 || !strings.Contains(out, "try again in a moment") {
			t.Fatalf("got %q (exit %d)", out, code)
		}

		if _, err := os.Stat(f.release()); err == nil {
			t.Fatal("downloaded in cached-only mode")
		}
	})

	t.Run("the hook puts the plugin's bin/ first, once", func(t *testing.T) {
		f := newShimFixture(t)
		f.write(f.release(), fakeBinary("cache"), 0o755)

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

	t.Run("without CLAUDE_ENV_FILE the hook names a shadowing f10", func(t *testing.T) {
		f := newShimFixture(t)
		f.write(f.release(), fakeBinary("cache"), 0o755)
		f.write(filepath.Join(f.stubs, "f10"), fakeBinary("stale"), 0o755)

		out, code := f.run("--shim-hook")
		if code != 0 || !strings.Contains(out, `"systemMessage":"f10: `+filepath.Join(f.stubs, "f10")+" comes before") {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("the hook says what is missing when neither curl nor go is there", func(t *testing.T) {
		f := newShimFixture(t)

		out, code := f.run("--shim-hook")
		if code != 0 || !strings.HasPrefix(out, `{"systemMessage":"f10: no binary for `+shimVersion) ||
			!strings.Contains(out, "curl is not installed, and go is not installed") {
			t.Fatalf("got %q (exit %d)", out, code)
		}
	})

	t.Run("the hook rebuilds a checkout build older than cli/", func(t *testing.T) {
		f := newShimFixture(t)
		f.stubGo()

		if out, code := f.run("--shim-rebuild"); code != 0 {
			t.Fatalf("--shim-rebuild: %q (exit %d)", out, code)
		}

		old := time.Now().Add(-time.Hour)
		must(t, os.Chtimes(f.build()+".built", old, old))
		f.write(filepath.Join(f.root, "cli", "main.go"), "package main\n", 0o644)

		record := filepath.Join(f.stubs, "ldflags")
		must(t, os.Remove(record))

		if out, code := f.run("--shim-hook"); code != 0 || out != "" {
			t.Fatalf("got %q (exit %d)", out, code)
		}

		eventually(t, "the background rebuild", func() bool {
			_, err := os.Stat(record)
			return err == nil
		})
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
