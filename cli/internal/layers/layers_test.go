package layers

import (
	"bufio"
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const module = "github.com/amberpixels/f10/cli/"

func TestModuleFollowsTheTable(t *testing.T) {
	t.Parallel()

	statSources(t)

	imports := map[string][]string{}

	// a test's imports couple packages too, and the unix-only and other
	// files differ in what they import, so both platforms are read
	for _, goos := range []string{"", "windows"} {
		cmd := exec.CommandContext(t.Context(), "go", "list", "-f",
			`{{.ImportPath}} {{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}`, module+"...")
		if goos != "" {
			cmd.Env = append(os.Environ(), "GOOS="+goos)
		}

		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list (GOOS=%s): %v", goos, err)
		}

		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			from := strings.TrimPrefix(fields[0], module)

			if _, ok := imports[from]; !ok {
				imports[from] = nil
			}

			for _, imp := range fields[1:] {
				if to, ok := strings.CutPrefix(imp, module); ok && !slices.Contains(imports[from], to) {
					imports[from] = append(imports[from], to)
				}
			}
		}
	}

	for _, v := range Check(imports, Table) {
		t.Error(v)
	}
}

func TestCheckReportsAReverseImport(t *testing.T) {
	t.Parallel()

	table := []Rule{
		{Prefix: "internal", Rank: 0, Peers: true},
		{Prefix: "internal/svc", Rank: 2},
		{Prefix: "internal/command", Rank: 2, Exact: true},
		{Prefix: "internal/command/a", Rank: 3},
		{Prefix: "internal/command/b", Rank: 3},
		{Prefix: "cmd/app", Rank: 9},
	}
	imports := map[string][]string{
		"cmd/app":            {"internal/command/a", "internal/gitx"},
		"internal/command/a": {"internal/svc", "internal/gitx"},
		"internal/svc":       {"internal/command/a"},
		"internal/command/b": {"internal/command/a"},
		"internal/gitx":      {"internal/fsx"},
		"internal/fsx":       nil,
		"other/unranked":     nil,
		"internal/command":   nil,
		// a command nobody ranked, which the broad internal rule would cover
		"internal/command/c": {"internal/gitx"},
	}

	got := Check(imports, table)

	want := []string{
		"internal/command/b imports internal/command/a: peers of rank 3 import each other; move the shared piece down instead",
		"internal/command/c: no rank in the layers table",
		"internal/svc imports internal/command/a: a service imports a command; move the helper down to a service",
		"other/unranked: no rank in the layers table",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d violations %v, want %v", len(got), got, want)
	}

	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("violation %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A leaf reaching into project state, and a service borrowing a command's
// helper, both fail under the real table.
func TestTableRejectsLeafAndCommandImports(t *testing.T) {
	t.Parallel()

	got := Check(map[string][]string{
		"internal/ui":           {"internal/state"},
		"internal/finish":       {"internal/command/task"},
		"internal/command/task": nil,
		"internal/state":        nil,
	}, Table)

	want := []string{
		"internal/finish imports internal/command/task: a service imports a command; move the helper down to a service",
		"internal/ui imports internal/state: rank -1 imports rank 0; move the shared piece down instead",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d violations %v, want %v", len(got), got, want)
	}

	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("violation %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEveryPackageCommentHasItsHome(t *testing.T) {
	t.Parallel()

	for _, p := range commentProblems(t, filepath.Join("..", "..")) {
		t.Error(p)
	}
}

func TestCommentRuleFlagsAStrayAndAMissingComment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files := map[string]string{
		"good/good.go":     "// Package good is fine.\npackage good\n",
		"good/other.go":    "package good\n",
		"doc/doc.go":       "// Package doc is fine.\npackage doc\n",
		"doc/x_unix.go":    "//go:build unix\n\npackage doc\n",
		"stray/stray.go":   "package stray\n",
		"stray/other.go":   "// Package stray sits in the wrong file.\npackage stray\n",
		"none/none.go":     "package none\n",
		"twice/twice.go":   "// Package twice is here.\npackage twice\n",
		"twice/doc.go":     "// Package twice is here too.\npackage twice\n",
		"cmd/app/main.go":  "// Command app is fine.\npackage main\n",
		"cmd/app/flags.go": "package main\n",
		"bin/run.go":       "// Command run belongs in main.go.\npackage main\n",
	}

	for name, src := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got := commentProblems(t, root)
	want := []string{
		"bin: package comment in run.go; it belongs in main.go",
		"none: no package comment; write one in none.go or doc.go",
		"stray: package comment in other.go; it belongs in stray.go or doc.go",
		"twice: package comment in doc.go and twice.go; keep one",
	}

	if !slices.Equal(got, want) {
		t.Errorf("problems = %q, want %q", got, want)
	}
}

// commentProblems checks the comment rule over every package under root: the
// comment lives in <dir>.go or doc.go, a binary's in main.go, and only there.
// Reading the files itself keeps go test's cache honest.
func commentProblems(t *testing.T, root string) []string {
	t.Helper()

	docs := map[string][]string{}
	mains := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && path != root && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")):
			return filepath.SkipDir
		case d.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go"):
			return nil
		}

		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return err
		}

		dir, _ := filepath.Rel(root, filepath.Dir(path))
		if _, ok := docs[dir]; !ok {
			docs[dir] = nil
		}

		mains[dir] = f.Name.Name == "main"

		if f.Doc != nil {
			docs[dir] = append(docs[dir], d.Name())
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for dir, files := range docs {
		dir = filepath.ToSlash(dir)
		home := []string{filepath.Base(dir) + ".go", "doc.go"}
		if mains[dir] {
			home = []string{"main.go"}
		}

		slices.Sort(files)

		switch {
		case len(files) == 0:
			out = append(out, dir+": no package comment; write one in "+strings.Join(home, " or "))
		case len(files) > 1:
			out = append(out, dir+": package comment in "+strings.Join(files, " and ")+"; keep one")
		case !slices.Contains(home, files[0]):
			out = append(out, dir+": package comment in "+files[0]+"; it belongs in "+strings.Join(home, " or "))
		}
	}

	slices.Sort(out)

	return out
}

// statSources touches every Go file in the module: go test caches a result
// against the files the test read, and it cannot see what go list read.
func statSources(t *testing.T) {
	t.Helper()

	root := filepath.Join("..", "..")

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}

		_, err = os.Stat(path)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
