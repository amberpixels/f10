package lookaround

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/storage"
)

func TestReadRootOnTheEdgeFixture(t *testing.T) {
	for _, tc := range []struct {
		files []string
		round int
		open  int
	}{
		// 1.3, 1.4 and 1.6; `asked` is not the word ask, so it reads as
		// settled, as in status and review round
		{files: []string{"1.md"}, round: 1, open: 3},
		// 1.3 reopened without a verdict; `ask+reply` is not the word ask
		{files: []string{"1.md", "2.md"}, round: 2, open: 1},
	} {
		root := t.TempDir()
		dir := filepath.Join(root, "reviews", "FT-1")

		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		for _, name := range tc.files {
			raw, err := os.ReadFile(filepath.Join("..", "findings", "testdata", "edge", name))
			if err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		l := &Local{
			Findings: map[string]Findings{},
			Plans:    map[string]time.Time{},
			Archived: map[string]bool{},
			Verdicts: map[string]string{},
		}
		readRoot(storage.Root(root), l)

		if got := l.Findings["FT-1"]; got.Round != tc.round || got.Open != tc.open {
			t.Errorf("%v: %+v, want round %d with %d open", tc.files, got, tc.round, tc.open)
		}
	}
}

// The board reads each key's highest-numbered file, and a file that does
// not parse becomes a note instead of a value.
func TestReadRootReadsTheLatestFiles(t *testing.T) {
	root := t.TempDir()

	write := func(path, text string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(root, "verdicts", "FT-1", "9.md"), "task: FT-1\nverdict: rethink\n")
	write(filepath.Join(root, "verdicts", "FT-1", "10.md"), "task: FT-1\nverdict: proceed\n")
	write(filepath.Join(root, "verdicts", "FT-2", "1.md"), "task: FT-2\nverdict: maybe\n")
	write(filepath.Join(root, "reviews", "FT-3", "1.md"), "no header\n")

	l := &Local{
		Findings: map[string]Findings{},
		Plans:    map[string]time.Time{},
		Archived: map[string]bool{},
		Verdicts: map[string]string{},
	}
	notes := readRoot(storage.Root(root), l)

	if l.Verdicts["FT-1"] != "proceed" {
		t.Errorf("FT-1 verdict = %q, want 10.md's proceed", l.Verdicts["FT-1"])
	}

	if _, ok := l.Verdicts["FT-2"]; ok || len(notes) != 2 {
		t.Errorf("verdicts %v, notes %q: want FT-2 and FT-3 as two notes", l.Verdicts, notes)
	}
}
