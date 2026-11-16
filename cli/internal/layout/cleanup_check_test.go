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
			"DASH IN CODE: a.go:5\ncomments: code unchanged", 0,
		},
		{
			"pointer line edit", "a.go", "package x\n\nfunc f(p *int) {\n\t*p = 1\n}\n",
			"package x\n\nfunc f(p *int) {\n\t*p = 2\n}\n",
			"CODE CHANGED: a.go", 1,
		},
		{
			"block comment rewrite", "a.go",
			"package x\n\n/*\n * Long story,\n * previously two.\n */\nvar n = 1 /* one */ + 1\n",
			"package x\n\n/*\n * Why: one.\n */\nvar n = 1 /* a one */ + 1\n",
			"code unchanged", 0,
		},
		{
			"comment marker in a string", "a.go", "package x\n\nvar u = \"http://a\" // site\n",
			"package x\n\nvar u = \"http://b\" // site\n",
			"CODE CHANGED: a.go", 1,
		},
		{
			"hash in a shell string", "b.sh", "echo \"a #b\" # note\n", "echo \"a #c\" # note\n",
			"CODE CHANGED: b.sh", 1,
		},
		{
			"shell parameter hash", "b.sh", "n=${#a} # count\n", "n=${#a} # length\n",
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

// proseCheck runs the prose mode in a git repo where name is committed as
// before and holds after in the working tree. With ref empty it passes --all.
// Untracked files are written but never committed.
func proseCheck(t *testing.T, ref, name, before, after string, untracked map[string]string) (string, int) {
	t.Helper()

	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(
			os.Environ(),
			"GIT_AUTHOR_NAME=t",
			"GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t",
		)

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q")
	write(name, before)
	run("add", name)
	run("commit", "-q", "-m", "before")
	write(name, after)

	files := []string{name}
	for path, body := range untracked {
		write(path, body)
		files = append(files, path)
	}

	if ref == "" {
		ref = "--all"
	}

	script := filepath.Join(filepath.Dir(scriptPath(t)), "f10-cleanup-check.sh")
	cmd := exec.Command(script, append([]string{"prose", ref}, files...)...)
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

func TestCleanupCheckProse(t *testing.T) {
	const (
		plain = "Keep this line short.\n"
		dense = "Run it here, there, again, later, then stop.\n"
		long  = "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty twenty-one twenty-two twenty-three twenty-four twenty-five twenty-six twenty-seven twenty-eight twenty-nine thirty thirty-one.\n"
	)

	cases := []struct {
		name, ref, file, before, after string
		untracked                      map[string]string
		want                           string
		code                           int
	}{
		{"semicolon", "HEAD", "p.md", plain, plain + "Do one thing; then another.\n", nil, "p.md:2: semicolon", 1},
		{"four joints", "HEAD", "p.md", plain, plain + dense, nil, "p.md:2: 4 joints", 1},
		{"31 words", "HEAD", "p.md", plain, plain + long, nil, "p.md:2: 31 words", 1},
		{
			"at the limits",
			"HEAD",
			"p.md",
			plain,
			plain + "Run it here, then there, and stop (once) with one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen.\n",
			nil,
			"no dense sentences",
			0,
		},
		{"unchanged dense line passes", "HEAD", "p.md", dense, dense + plain, nil, "no dense sentences", 0},
		{"--all checks every line", "", "p.md", dense, dense + plain, nil, "p.md:1: 4 joints", 1},
		{
			"wrapped sentence flags at its first line", "HEAD", "p.md", "Run it here,\nthen there.\n",
			"Run it here,\nthere, again, later, then stop.\n", nil, "p.md:1: 4 joints", 1,
		},
		{
			"skipped markdown",
			"HEAD",
			"p.md",
			plain,
			"---\ndescription: a, b, c, d, e; f\n---\n" + plain + "```\na, b, c, d; e\n```\n| a, b | c, d, e; |\n# A, b, c, d, e\n" +
				"Run `a, b, c; d` and see [the docs](https://x.io/a:b) now.\n",
			nil,
			"no dense sentences",
			0,
		},
		{
			"list label",
			"HEAD",
			"p.md",
			plain,
			plain + "- **name** - run it here, then there, and stop.\n",
			nil,
			"no dense sentences",
			0,
		},
		{
			"key label",
			"HEAD",
			"p.md",
			plain,
			plain + "- `a`, `b` and `c`: run it here, then there, and stop.\n",
			nil,
			"no dense sentences",
			0,
		},
		{
			"bold sentence ends",
			"HEAD",
			"p.md",
			plain,
			plain + "**Stop here.** Then a, b, c, d.\n",
			nil,
			"no dense sentences",
			0,
		},
		{
			"go comments",
			"HEAD",
			"a.go",
			"package x\n",
			"package x\n\n// Run it here, there, again, later, then stop.\nvar s = \"a, b, c, d; e\"\n\nvar n = 1 // a, b; c\n",
			nil,
			"a.go:3: 4 joints",
			1,
		},
		{
			"trailing comment",
			"HEAD",
			"a.go",
			"package x\n",
			"package x\n\nvar n = 1 // a, b; c\n",
			nil,
			"a.go:3: semicolon",
			1,
		},
		{
			"unclosed quote hides no comment",
			"HEAD",
			"b.sh",
			"x=1\n",
			"x=1\ncat <<EOF\nit's\nEOF\n# a, b; c\n",
			nil,
			"b.sh:5: semicolon",
			1,
		},
		{
			"untracked file in full",
			"HEAD",
			"p.md",
			plain,
			plain,
			map[string]string{"new.md": dense},
			"new.md:1: 4 joints",
			1,
		},
		{"unknown syntax", "HEAD", "p.md", plain, plain, map[string]string{"c.txt": "a\n"}, "UNCHECKED: c.txt", 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := proseCheck(t, c.ref, c.file, c.before, c.after, c.untracked)
			if code != c.code || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q; want exit %d and %q", code, out, c.code, c.want)
			}
		})
	}
}
