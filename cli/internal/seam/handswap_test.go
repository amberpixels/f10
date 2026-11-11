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

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || as.Tok != token.ASSIGN {
					return true
				}

				for _, lhs := range as.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok {
						continue
					}

					if pkg, ok := sel.X.(*ast.Ident); ok && imported[pkg.Name] && sel.Sel.IsExported() {
						t.Errorf("%s: %s.%s assigned by hand; use seam.Swap",
							fset.Position(as.Pos()), pkg.Name, sel.Sel.Name)
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
