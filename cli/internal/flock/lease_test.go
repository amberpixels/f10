//go:build unix

package flock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLeaseIsExclusiveAndNamesItsHolder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "locks", "x.lock")

	l, err := Acquire(path, "pid=1 drive=2")
	if err != nil {
		t.Fatal(err)
	}

	// flock conflicts per open file, so a second open in this process counts
	_, err = Acquire(path, "pid=3")

	var held HeldError
	if !errors.As(err, &held) || held.Holder != "pid=1 drive=2" {
		t.Fatalf("second Acquire = %v, want a HeldError naming the first holder", err)
	}

	if line, ok := Probe(path); !ok || line != "pid=1 drive=2" {
		t.Errorf("Probe while held = %q, %v", line, ok)
	}

	l.Release()
	l.Release()

	if _, ok := Probe(path); ok {
		t.Error("Probe after release reads held")
	}

	if _, ok := Probe(filepath.Join(t.TempDir(), "never.lock")); ok {
		t.Error("a lock never taken reads held")
	}
}
