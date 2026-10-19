package layout

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanupCheck runs bin/f10-cleanup-check.sh in dir over one file whose
// snapshot holds before and whose current content is after.
func cleanupCheck(t *testing.T, mode, name, before, after string, extra map[string]string) (string, int) {
	t.Helper()

	dir := t.TempDir()
	write := func(path, body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(dir, "snap", name), before)
	write(filepath.Join(dir, name), after)

	files := []string{name}
	for path, body := range extra {
		write(filepath.Join(dir, path), body)
		if !strings.HasPrefix(path, "snap/") {
			files = append(files, path)
		}
	}

	script := filepath.Join(filepath.Dir(scriptPath(t)), "f10-cleanup-check.sh")
	cmd := exec.Command(script, append([]string{mode, "snap"}, files...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	code := 0

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("f10-cleanup-check.sh: %v", err)
	}

	return string(out), code
}

func TestCleanupCheckComments(t *testing.T) {
	const goBefore = "package x\n\n// Foo does a thing, previously bar.\n// It used to return two.\nfunc Foo() int { return 1 } // one\n\nvar s = \"a \u2014 b\"\n"

	cases := []struct {
		name, file, before, after string
		want                      string
		code                      int
	}{
		{
			"comment rewrite", "a.go", goBefore,
			"package x\n\n// Foo returns one.\nfunc Foo() int { return 1 } // one\n\nvar s = \"a \u2014 b\"\n",
			"code unchanged", 0,
		},
		{
			"trailing comment reword", "a.go", goBefore,
			"package x\n\nfunc Foo() int { return 1 } // the answer\n\nvar s = \"a \u2014 b\"\n",
			"code unchanged", 0,
		},
		{
			"dash swap in a string", "a.go", goBefore,
			"package x\n\nfunc Foo() int { return 1 }\n\nvar s = \"a - b\"\n",
			"code unchanged", 0,
		},
		{
			"code edit", "a.go", goBefore,
			"package x\n\nfunc Foo() int { return 2 }\n\nvar s = \"a \u2014 b\"\n",
			"CODE CHANGED: a.go", 1,
		},
		{
			"shell comment", "b.sh", "x=1 # one\n# a note\ny=2\n", "x=1 # two\ny=2\n",
			"code unchanged", 0,
		},
		{"unknown syntax", "c.txt", "a\n", "a\n", "UNCHECKED: c.txt", 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := cleanupCheck(t, "comments", c.file, c.before, c.after, nil)
			if code != c.code || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q; want exit %d and %q", code, out, c.code, c.want)
			}
		})
	}
}

func TestCleanupCheckLiterals(t *testing.T) {
	const before = "Run `just lint` with --fast, see docs/setup.md, within 30 seconds.\n\n```bash\nmake all\n```\n"

	cases := []struct {
		name, after string
		extra       map[string]string
		want        string
		code        int
	}{
		{
			"all kept", "Run `just lint --fast` (docs/setup.md), 30 seconds.\n\n```bash\nmake all\n```\n",
			nil, "all kept", 0,
		},
		{
			"literal lost", "Run `just lint` within 30 seconds.\n\n```bash\nmake all\n```\n",
			nil, "MISSING in p.md:\n  --fast\n  docs/setup.md", 1,
		},
		{
			"moved to another file", "Run `just lint` within 30 seconds.\n\n```bash\nmake all\n```\n",
			map[string]string{"home.md": "Use --fast; docs/setup.md has the rest.\n"},
			"all kept", 0,
		},
		{
			"dropped on purpose", "Run `just lint` within 30 seconds.\n\n```bash\nmake all\n```\n",
			map[string]string{"snap/.dropped": "--fast\ndocs/setup.md\n"},
			"all kept", 0,
		},
		{
			"code line lost", "Run `just lint` with --fast, see docs/setup.md, within 30 seconds.\n",
			nil, "make all", 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := cleanupCheck(t, "literals", "p.md", before, c.after, c.extra)
			if code != c.code || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q; want exit %d and %q", code, out, c.code, c.want)
			}
		})
	}
}
