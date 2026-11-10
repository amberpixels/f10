package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/storage"
)

const handoffBody = "## Config\n- set `LOCALE_DEFAULT` before the deploy"

func writeHandoffFile(t *testing.T, path, head string) {
	t.Helper()

	writeFile(t, path, "---\ntask: GH-1\nhead: "+head+"\nwritten: 2026-11-04T10:15:00Z\n---\n\n"+handoffBody+"\n")
}

func tipOf(sha string) func() string { return func() string { return sha } }

func TestHandoffLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "GH-1.md")

	missing := strings.Join(handoffLines(path, "GH-1", tipOf("abc")), "\n")
	if !strings.HasPrefix(missing, "no handoff for GH-1:") || !strings.Contains(missing, "/f10:handoff") {
		t.Errorf("missing = %q", missing)
	}

	writeHandoffFile(t, path, "abc1234567")

	if got := strings.Join(handoffLines(path, "GH-1", tipOf("abc1234567")), "\n"); got != handoffBody {
		t.Errorf("current = %q, want the body alone", got)
	}

	got := handoffLines(path, "GH-1", tipOf("def7654321"))
	if len(got) < 2 || !strings.Contains(got[0], "written at abc1234, the branch merged at def7654") ||
		strings.Join(got[1:], "\n") != handoffBody {
		t.Errorf("stale = %q", got)
	}

	// a head copied short is its own commit, not a stale one
	writeHandoffFile(t, path, "abc1234")

	if got := strings.Join(handoffLines(path, "GH-1", tipOf("abc1234567")), "\n"); got != handoffBody {
		t.Errorf("short head = %q, want the body alone", got)
	}

	writeHandoffFile(t, path, "abc1234567")

	// no tip to compare against prints the body as it is
	if got := strings.Join(handoffLines(path, "GH-1", tipOf("")), "\n"); got != handoffBody {
		t.Errorf("no tip = %q", got)
	}

	writeFile(t, path, "## Config\n- no frontmatter\n")

	if got := handoffLines(path, "GH-1", tipOf("abc")); len(got) != 1 || !strings.Contains(got[0], "no frontmatter") {
		t.Errorf("unreadable = %q", got)
	}
}

// archiveHandoff is the move finish and the janitor make, in one call.
func archiveHandoff(root storage.Root, id string) (string, error) {
	src, target := handoffTarget(root, id)

	return target, moveHandoff(src, target)
}

func TestArchiveHandoffNumbers(t *testing.T) {
	main := t.TempDir()
	src := archive.TaskHandoff(storage.InRepo(main), "GH-1")
	archived := filepath.Join(filepath.Dir(src), "archive")

	if got, err := archiveHandoff(storage.InRepo(main), "GH-1"); err != nil || got != "" {
		t.Fatalf("missing file = %q, %v", got, err)
	}

	writeFile(t, filepath.Join(archived, "GH-1.md"), "first PR")
	writeHandoffFile(t, src, "abc")

	got, err := archiveHandoff(storage.InRepo(main), "GH-1")
	if err != nil || got != filepath.Join(archived, "GH-1.1.md") || fsx.Exists(src) {
		t.Fatalf("archived = %q, %v", got, err)
	}

	if body, _ := os.ReadFile(got); !strings.Contains(string(body), handoffBody) {
		t.Errorf("archived body = %q", body)
	}
}
