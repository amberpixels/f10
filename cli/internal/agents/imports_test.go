package agents_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestOnlyAgentsImportsHerdr keeps Herdr behind the agents core, so the
// generic agents layer can be split out of f10 later. It parses imports
// rather than running go list, so it needs no toolchain subprocess.
func TestOnlyAgentsImportsHerdr(t *testing.T) {
	const herdr = "github.com/amberpixels/f10/cli/internal/herdr"

	root := filepath.Join("..", "..")
	allowed := []string{filepath.Join("internal", "agents"), filepath.Join("internal", "herdr")}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}

		if rel, _ := filepath.Rel(root, filepath.Dir(path)); slices.Contains(allowed, rel) {
			return nil
		}

		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == herdr {
				t.Errorf("%s imports %s: reach agents through internal/agents", path, herdr)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
