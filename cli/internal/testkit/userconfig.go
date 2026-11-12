package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

func WriteUserFile(t *testing.T, home, name, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(home, ".f10"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(home, ".f10", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
