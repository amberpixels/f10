package flock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A HeldError is a lease another holder has; Holder is the line it wrote.
type HeldError struct {
	Path   string
	Holder string
}

func (e HeldError) Error() string { return e.Path + " is held: " + e.Holder }

// A Lease is a held lock file, released by Release or by the process ending.
// Lock files are never deleted: unlinking a held one lets a second process
// lock a fresh inode under the same name.
type Lease struct {
	f *os.File
}

// leaseRetries absorb a reader's Probe, which holds the lock for an instant.
var (
	leaseRetries    = 4
	leaseRetryDelay = 50 * time.Millisecond
)

// Acquire takes path's lease without waiting on a holder, writing holder
// into the file for Probe to read; a held one is a HeldError naming it.
func Acquire(path, holder string) (*Lease, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("lease %s: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lease %s: %w", path, err)
	}

	for i := 0; ; i++ {
		err = Exclusive(f)
		if err == nil || !errors.Is(err, ErrHeld) || i >= leaseRetries {
			break
		}

		time.Sleep(leaseRetryDelay)
	}

	if errors.Is(err, ErrHeld) {
		_ = f.Close()

		return nil, HeldError{Path: path, Holder: readHolder(path)}
	}

	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("lease %s: %w", path, err)
	}

	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(holder+"\n"), 0) // the holder is a view; the lock is what owns
	}

	return &Lease{f: f}, nil
}

// Release drops the lease; nil and a second call are fine.
func (l *Lease) Release() {
	if l == nil || l.f == nil {
		return
	}

	_ = Unlock(l.f)
	_ = l.f.Close()
	l.f = nil
}

// Probe reports the holder line of path's lease, false when nobody holds
// it. It takes a shared lock for an instant, which Acquire's retries absorb.
func Probe(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	if err := Shared(f); err != nil {
		if errors.Is(err, ErrHeld) {
			return readHolder(path), true
		}

		return "", false
	}

	_ = Unlock(f)

	return "", false
}

// readHolder is the line a holder wrote, "" when unreadable.
func readHolder(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(b))
}
