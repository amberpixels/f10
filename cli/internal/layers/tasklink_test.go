package layers_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/layers"
)

// plainIDPrint is a print whose argument is a bare task id: a local id, a
// ref's ID, a row's ID, an ask's Task or a bundle's Label(). ui.Exit is left
// out: its message is an error on stderr, which stays plain.
var plainIDPrint = regexp.MustCompile(
	`(fmt\.Fprint\w*|ui\.Warnf)\([^,]+, "[^"]*"(, [^,()]+)*, (id|[\w.]*ref\.ID|[\w.]*\.ID|[\w.]*\.Task|[\w.]*\.Label\(\))[,)]`,
)

// machineRead are prints of a bare id that agents or scripts parse, by file
// (relative to the module root) and a piece of the line, so they stay plain
// on purpose.
var machineRead = map[string]string{
	"internal/hostops/host.go":               `"# %s: %s\n\n"`,                // the task as markdown, for an agent
	"internal/command/task/taskwrite.go":     `"%s  %s  ->  %s\n"`,            // retitle's change list, plain since it was written
	"internal/command/review/reviewround.go": `"  %s %s - %s\n"`,              // a declined round, read by the review step
	"internal/command/review/review.go":      `"%s  %s  %s\n"`,                // review pick, parsed
	"internal/command/pr/pr.go":              `"task: %s\n"`,                  // pr view, parsed
	"internal/statusview/refs.go":            `"%s: %v"`,                      // a warning on stderr
	"internal/command/task/taskreport.go":    `"%s: %v: no subtasks to list"`, // a warning on stderr
}

// commandSources are the non-test files of the command tree: cmd/f10 and
// every package the layers table ranks above the domain packages.
func commandSources(t *testing.T) []string {
	t.Helper()

	root := filepath.Join("..", "..")

	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return err
		}

		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		if r, ok := layers.RankOf(
			filepath.ToSlash(filepath.Dir(rel)),
			layers.Table,
		); ok && r.Rank > 0 &&
			!strings.HasPrefix(rel, "cmd/f10eval/") {
			files = append(files, rel)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(files) == 0 {
		t.Fatal("no command sources found")
	}

	return files
}

// The render table above covers the commands that exist; this scan catches a
// new print of a bare task id in any file, which must go through TaskLinks or
// be listed in machineRead.
func TestNoPlainTaskIDPrints(t *testing.T) {
	used := map[string]bool{}

	t.Cleanup(func() {
		for f := range machineRead {
			if !used[f] {
				t.Errorf("machineRead names %s, but no print there matches it: move the entry with its line", f)
			}
		}
	})

	for _, f := range commandSources(t) {
		src, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatal(err)
		}

		for i, line := range strings.Split(string(src), "\n") {
			if !plainIDPrint.MatchString(line) {
				continue
			}

			if allowed, ok := machineRead[f]; ok && strings.Contains(line, allowed) {
				used[f] = true

				continue
			}

			t.Errorf("%s:%d prints a task id plain; print it through taskLinks:\n%s", f, i+1, strings.TrimSpace(line))
		}
	}
}
