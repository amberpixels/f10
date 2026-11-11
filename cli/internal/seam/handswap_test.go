package seam

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A test that sets another package's variable by hand skips Swap's guard
// against t.Parallel, so every such swap goes through Swap. TestMain is
// exempt: it sets the binary's fixtures once, before any test runs.
func TestNoTestSwapsAPackageVariableByHand(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	owned := packageVars(t, fset, root)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}

		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		imported := map[string]bool{}

		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			name := filepath.Base(p)

			if imp.Name != nil {
				name = imp.Name.Name
			}

			imported[name] = true
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "TestMain" {
				continue
			}

			// the package's own variables, which its internal tests name bare
			own := owned[filepath.Dir(path)]

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || as.Tok != token.ASSIGN {
					return true
				}

				for _, lhs := range as.Lhs {
					switch x := lhs.(type) {
					case *ast.SelectorExpr:
						if pkg, ok := x.X.(*ast.Ident); ok && imported[pkg.Name] && x.Sel.IsExported() {
							t.Errorf("%s: %s.%s assigned by hand; use seam.Swap",
								fset.Position(as.Pos()), pkg.Name, x.Sel.Name)
						}
					case *ast.Ident:
						if own[x.Name] {
							t.Errorf("%s: %s assigned by hand; use seam.Swap", fset.Position(as.Pos()), x.Name)
						}
					}
				}

				return true
			})
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// packageVars is each directory's exported package-level variables, read
// from its non-test files.
func packageVars(t *testing.T, fset *token.FileSet, root string) map[string]map[string]bool {
	t.Helper()

	out := map[string]map[string]bool{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}

		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}

			for _, spec := range gd.Specs {
				for _, name := range spec.(*ast.ValueSpec).Names {
					if name.IsExported() {
						if out[filepath.Dir(path)] == nil {
							out[filepath.Dir(path)] = map[string]bool{}
						}

						out[filepath.Dir(path)][name.Name] = true
					}
				}
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}
