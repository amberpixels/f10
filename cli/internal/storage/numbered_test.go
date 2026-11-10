package storage

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNumbered(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, name := range []string{"10.md", "9.md", "notes.md", "2.txt", "1.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Mkdir(filepath.Join(dir, "3.md"), 0o750); err != nil {
		t.Fatal(err)
	}

	got := Numbered(dir)
	for i := range got {
		got[i] = filepath.Base(got[i])
	}

	if want := []string{"1.md", "9.md", "10.md"}; !slices.Equal(got, want) {
		t.Errorf("Numbered = %v, want %v", got, want)
	}

	if len(Numbered(filepath.Join(dir, "missing"))) != 0 {
		t.Error("a missing dir lists files")
	}

	if NumberOf(filepath.Join(dir, "10.md")) != 10 || NumberOf("notes.md") != -1 || NumberOf("4.txt") != -1 {
		t.Error("NumberOf misreads a name")
	}
}
