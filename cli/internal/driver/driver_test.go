package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// storageWith links one testdata driver into a temp storage root; the
// fixtures are files, not strings, because the exec bit is what Find tests.
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

// branch returns a name and never creates it: the one verb whose answer
// f10 acts on with git rather than with a browser.
func TestRunBranch(t *testing.T) {
	root := storageWith(t, "ok-driver")

	out, err := Find(root, root).Run(t.Context(), VerbBranch, "1042")
	if err != nil {
		t.Fatalf("Run(branch): %v", err)
	}

	if want := "ABC-1042/short-slug"; out != want {
		t.Errorf("Run(branch) = %q, want %q", out, want)
	}
}

// The reserved code is a normal answer, not a failure: a driver may offer
// read without search.
func TestRunUnsupportedVerb(t *testing.T) {
	root := storageWith(t, "ok-driver")

	for _, verb := range []string{VerbSearch, VerbChildren} {
		_, err := Find(root, root).Run(t.Context(), verb, "anything")
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Run(%s) = %v, want ErrUnsupported", verb, err)
		}
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

// A write verb reads the task from stdin and takes its properties as
// flags; a warning on stderr comes back beside the answer.
func TestRunInputCreate(t *testing.T) {
	root := storageWith(t, "ok-driver")

	out, warn, err := Find(
		root,
		root,
	).RunInput(t.Context(), "# Add the index\n\nbody\n", VerbCreate, "--label", "draft")
	if err != nil {
		t.Fatalf("RunInput(create): %v", err)
	}

	if out != "1043" {
		t.Errorf("RunInput(create) = %q, want 1043", out)
	}

	if want := "created # Add the index with --label draft"; warn != want {
		t.Errorf("RunInput(create) stderr = %q, want %q", warn, want)
	}
}

// The reserved code and a hard failure classify for a write verb exactly as
// for a read.
func TestRunInputFailures(t *testing.T) {
	_, _, err := Find(storageWith(t, "ok-driver"), t.TempDir()).RunInput(t.Context(), "# x\n", VerbSearch)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("RunInput(declined) = %v, want ErrUnsupported", err)
	}

	_, _, err = Find(storageWith(t, "failing-driver"), t.TempDir()).RunInput(t.Context(), "# x\n", VerbCreate)
	if err == nil || errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "notion token missing") {
		t.Errorf("RunInput(failing) = %v, want the driver's stderr", err)
	}
}

// The vocabulary is ordered and case-insensitive; a word outside it has no
// rank, so a caller can drop it with a warning rather than guess.
func TestPriorityRank(t *testing.T) {
	for value, want := range map[string]int{"urgent": 0, "High": 1, "medium": 2, "LOW": 3, "low\n": 3} {
		if got, ok := PriorityRank(value); !ok || got != want {
			t.Errorf("PriorityRank(%q) = %d, %v; want %d", value, got, ok, want)
		}
	}

	for _, value := range []string{"", "p1", "highest"} {
		if got, ok := PriorityRank(value); ok || got != -1 {
			t.Errorf("PriorityRank(%q) = %d, %v; want -1, false", value, got, ok)
		}
	}
}

// hanging is the hanging-driver fixture with a limit far below its sleeps.
func hanging(t *testing.T) *Driver {
	t.Helper()

	d := Find(storageWith(t, "hanging-driver"), t.TempDir())
	d.Timeout = 100 * time.Millisecond

	return d
}

// A verb past its limit is ErrTimeout naming the driver, the verb and the
// limit, returned long before the driver would have answered.
func TestRunTimeout(t *testing.T) {
	start := time.Now()

	_, err := hanging(t).Run(t.Context(), VerbRead, "22")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Run(read) = %v, want ErrTimeout", err)
	}

	if want := "driver read: no answer within 100ms"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Run took %s, want well under the driver's 10s sleep", took)
	}
}

// A write past its limit says it may have landed, since it is never retried.
func TestRunInputTimeout(t *testing.T) {
	_, _, err := hanging(t).RunInput(t.Context(), "# x\n", VerbCreate)
	if !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "may still have reached the tracker") {
		t.Errorf("RunInput(create) = %v, want ErrTimeout saying the write may have landed", err)
	}
}

// A background child still holding stdout cannot keep a killed driver, or
// one that already answered, from returning.
func TestLeftoverChildDoesNotBlock(t *testing.T) {
	start := time.Now()

	if _, err := hanging(t).Run(t.Context(), VerbSearch, "x"); !errors.Is(err, ErrTimeout) {
		t.Errorf("Run(search) = %v, want ErrTimeout", err)
	}

	d := hanging(t)
	d.Timeout = 0

	out, err := d.Run(t.Context(), VerbURL, "22")
	if err != nil || out != "https://example.test/22" {
		t.Errorf("Run(url) = %q, %v; want the url", out, err)
	}

	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("both runs took %s, want the pipes cut off well before the child's 10s sleep", took)
	}
}

// Only the verb's own deadline is a timeout; a caller cancelling is not.
func TestCancelledParentIsNotTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := hanging(t).Run(ctx, VerbRead, "22"); err == nil || errors.Is(err, ErrTimeout) {
		t.Errorf("Run with a cancelled parent = %v, want a non-timeout error", err)
	}
}

// A verb that exited 0 answered, even when its limit passed while a child
// it left behind still held stdout: a write reported as timed out would be
// retried into a duplicate.
func TestAnswerBeforeTheLimitIsNotATimeout(t *testing.T) {
	out, err := hanging(t).Run(t.Context(), VerbURL, "22")
	if err != nil || out != "https://example.test/22" {
		t.Errorf("Run(url) with its limit inside the wait for the child = %q, %v; want the url", out, err)
	}
}
