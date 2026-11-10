package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "run.state")

	if Exists(path) {
		t.Fatal("Exists before the write")
	}

	for _, body := range []string{"first\n", "second\n"} {
		if err := WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := os.ReadFile(path)
		if err != nil || string(got) != body {
			t.Fatalf("read %q, %v; want %q", got, err, body)
		}
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, %v; want 0600", info.Mode().Perm(), err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want the file alone", len(entries))
	}
}

func TestWriteFileMissingDir(t *testing.T) {
	t.Parallel()

	if err := WriteFile(filepath.Join(t.TempDir(), "no", "such"), nil, 0o600); err == nil {
		t.Error("a missing directory wrote")
	}
}
