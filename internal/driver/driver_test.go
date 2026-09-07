package driver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storageWith links one testdata driver into a temp storage root, which is
// where Find looks. The fixtures live in testdata because a driver is an
// executable, and the exec bit is the thing being tested.
func storageWith(t *testing.T, fixture string) string {
	t.Helper()

	root := t.TempDir()

	src, err := filepath.Abs(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(src, filepath.Join(root, "driver")); err != nil {
		t.Fatal(err)
	}

	return root
}

func TestFindAbsentAndNonExecutable(t *testing.T) {
	root := t.TempDir()

	if d := Find(root, root); d != nil {
		t.Errorf("Find with no driver = %+v, want nil", d)
	}

	if err := os.WriteFile(filepath.Join(root, "driver"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if d := Find(root, root); d != nil {
		t.Errorf("Find with a non-executable driver = %+v, want nil", d)
	}
}

func TestRunSucceeds(t *testing.T) {
	root := storageWith(t, "ok-driver")

	d := Find(root, root)
	if d == nil {
		t.Fatal("Find returned nil for an executable driver")
	}

	out, err := d.Run(t.Context(), VerbRead, "22")
	if err != nil {
		t.Fatalf("Run(read): %v", err)
	}

	if want := "# read 22"; out != want {
		t.Errorf("Run(read) = %q, want %q", out, want)
	}
}

// The reserved code is a normal answer, not a failure: a driver may
// implement read without search, and the caller falls back rather than
// erroring out.
func TestRunUnsupportedVerb(t *testing.T) {
	root := storageWith(t, "ok-driver")

	_, err := Find(root, root).Run(t.Context(), VerbSearch, "anything")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Run(search) = %v, want ErrUnsupported", err)
	}
}

// A real failure carries the driver's own stderr, because the driver knows
// why it failed and f10 does not.
func TestRunFailureKeepsStderr(t *testing.T) {
	root := storageWith(t, "failing-driver")

	_, err := Find(root, root).Run(t.Context(), VerbRead, "22")
	if err == nil {
		t.Fatal("Run against a failing driver returned no error")
	}

	if errors.Is(err, ErrUnsupported) {
		t.Fatalf("a hard failure was classified as unsupported: %v", err)
	}

	if !strings.Contains(err.Error(), "notion token missing") {
		t.Errorf("Run error = %q, want it to carry the driver's stderr", err)
	}
}
