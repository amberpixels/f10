package testkit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/facts"
)

// WriteVerdict puts a verdict file under root, its mtime age before now.
func WriteVerdict(t *testing.T, root, task, n, subject, verdict string, at time.Time) string {
	t.Helper()

	path := filepath.Join(root, "verdicts", task, n+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	body := "task: " + task + "\nsubject: " + subject + "\nverdict: " + verdict + "\nreason: r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}

	return path
}

// StaleOf is a stale_after value as the frontmatter writes it; "" is off.
func StaleOf(value string) facts.StaleAfter {
	var s facts.StaleAfter
	_ = s.UnmarshalText([]byte(value))

	return s
}
