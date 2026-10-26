package lore

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The package must stay extractable: nothing from the rest of the module.
func TestImportsNothingInternal(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".go") {
			continue
		}

		parsed, err := parser.ParseFile(fset, f.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}

		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, "github.com/amberpixels/f10/") {
				t.Errorf("%s imports %s", f.Name(), path)
			}
		}
	}
}
