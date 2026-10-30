// Package drives keeps `f10 drive` durable: a record per drive under the
// storage root, and a lease per task, so two drives, or a drive and
// `f10 finish`, never act on one task at once.
package drives

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Dir is the drives directory under a storage root.
const Dir = "drives"

// A Holder is who holds a lease, as its lock file names it.
type Holder struct {
	PID   int
	Drive int  // 0 for `f10 finish` itself
	Watch bool // the daemon's watcher, finishing a task no drive owns
}

// String names the holder for a refusal: `drive 3 (pid 4242)`.
func (h Holder) String() string {
	who := "f10 finish"

	switch {
	case h.Drive > 0:
		who = "drive " + strconv.Itoa(h.Drive)
	case h.Watch:
		who = "the f10 watcher"
	}

	if h.PID > 0 {
		return fmt.Sprintf("%s (pid %d)", who, h.PID)
	}

	return who
}

// A HeldError is a lease another process holds.
type HeldError struct {
	Key    string
	Holder Holder
}

func (e HeldError) Error() string {
	return e.Key + " is owned by " + e.Holder.String()
}

// A Lease is a held lock, released by Release or by the process ending.
type Lease struct {
	f *os.File
}

// retries absorb a reader's probe, which holds the lock for an instant.
var (
	retries    = 4
	retryDelay = 50 * time.Millisecond
)

// lockPath is key's lock file. Lock files are never deleted: unlinking a
// held one lets a second process lock a fresh inode under the same name.
func lockPath(root, key string) string {
	return filepath.Join(root, Dir, "locks", strings.ReplaceAll(key, "/", "_")+".lock")
}

// Acquire takes key's lease for drive (0 for finish) under the storage
// root, without waiting on a holder: a held one is a HeldError naming it.
func Acquire(root, key string, drive int) (*Lease, error) {
	return AcquireAs(root, key, Holder{Drive: drive})
}

// AcquireAs takes key's lease as h, whose PID is always this process.
func AcquireAs(root, key string, h Holder) (*Lease, error) {
	path := lockPath(root, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("lease %s: %w", key, err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lease %s: %w", key, err)
	}

	for i := 0; ; i++ {
		err = lockExclusive(f)
		if err == nil || !errors.Is(err, errHeld) || i >= retries {
			break
		}

		time.Sleep(retryDelay)
	}

	if errors.Is(err, errHeld) {
		h := readHolder(path)
		_ = f.Close()

		return nil, HeldError{Key: key, Holder: h}
	}

	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("lease %s: %w", key, err)
	}

	body := "pid=" + strconv.Itoa(os.Getpid())
	if h.Drive > 0 {
		body += " drive=" + strconv.Itoa(h.Drive)
	}

	if h.Watch {
		body += " watch=1"
	}

	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(body+"\n"), 0) // the holder is a view; the lock is what owns
	}

	return &Lease{f: f}, nil
}

// awaitPoll is how often Await tries a held lease again.
const awaitPoll = 100 * time.Millisecond

// Await takes key's lease as h, waiting while another process holds it,
// until ctx ends or wait passes; then the HeldError names the holder.
func Await(ctx context.Context, root, key string, h Holder, wait time.Duration) (*Lease, error) {
	deadline := time.Now().Add(wait)

	for {
		l, err := AcquireAs(root, key, h)
		if held := (HeldError{}); !errors.As(err, &held) || !time.Now().Before(deadline) {
			return l, err
		}

		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(awaitPoll):
		}
	}
}

// Release drops the lease; nil and a second call are fine.
func (l *Lease) Release() {
	if l == nil || l.f == nil {
		return
	}

	_ = unlock(l.f)
	_ = l.f.Close()
	l.f = nil
}

// Probe reports who holds key's lease, false when nobody does. It takes a
// shared lock for an instant, which Acquire's retries absorb.
func Probe(root, key string) (Holder, bool) {
	path := lockPath(root, key)

	f, err := os.Open(path)
	if err != nil {
		return Holder{}, false
	}
	defer f.Close()

	if err := lockShared(f); err != nil {
		if errors.Is(err, errHeld) {
			return readHolder(path), true
		}

		return Holder{}, false
	}

	_ = unlock(f)

	return Holder{}, false
}

// readHolder parses a lock file; zero values for anything unreadable.
func readHolder(path string) Holder {
	b, err := os.ReadFile(path)
	if err != nil {
		return Holder{}
	}

	var h Holder

	for field := range strings.FieldsSeq(string(b)) {
		k, v, _ := strings.Cut(field, "=")
		n, _ := strconv.Atoi(v)

		switch k {
		case "pid":
			h.PID = n
		case "drive":
			h.Drive = n
		case "watch":
			h.Watch = n == 1
		}
	}

	return h
}

// DriveKey is a drive's own lease, held for the life of its process.
func DriveKey(n int) string { return "drive-" + strconv.Itoa(n) }
