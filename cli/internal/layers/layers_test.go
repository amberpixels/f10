package layers

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/amberpixels/f10/cli/"

func TestModuleFollowsTheTable(t *testing.T) {
	statSources(t)

	out, err := exec.CommandContext(t.Context(), "go", "list", "-f",
		"{{.ImportPath}} {{join .Imports \" \"}}", module+"...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	imports := map[string][]string{}

	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		from := strings.TrimPrefix(fields[0], module)
		imports[from] = nil

		for _, imp := range fields[1:] {
			if to, ok := strings.CutPrefix(imp, module); ok {
				imports[from] = append(imports[from], to)
			}
		}
	}

	for _, v := range Check(imports, Table) {
		t.Error(v)
	}
}

func TestCheckReportsAReverseImport(t *testing.T) {
	table := []Rule{
		{Prefix: "internal", Rank: 0, Peers: true},
		{Prefix: "internal/svc", Rank: 2},
		{Prefix: "internal/command", Rank: 3},
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
	}

	got := Check(imports, table)

	want := []string{
		"internal/command/b imports internal/command/a: peers of rank 3 import each other; move the shared piece down instead",
		"internal/svc imports internal/command/a: rank 2 imports rank 3; move the shared piece down instead",
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
