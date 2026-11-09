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

	"github.com/amberpixels/f10/cli/internal/flock"
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

// line is h as its lock file holds it, its PID always this process.
func (h Holder) line() string {
	body := "pid=" + strconv.Itoa(os.Getpid())
	if h.Drive > 0 {
		body += " drive=" + strconv.Itoa(h.Drive)
	}

	if h.Watch {
		body += " watch=1"
	}

	return body
}

// parseHolder reads a lock file's line; zero values for anything unreadable.
func parseHolder(line string) Holder {
	var h Holder

	for field := range strings.FieldsSeq(line) {
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

// A HeldError is a lease another process holds.
type HeldError struct {
	Key    string
	Holder Holder
}

func (e HeldError) Error() string {
	return e.Key + " is owned by " + e.Holder.String()
}

// A Lease is a held lock, released by Release or by the process ending.
type Lease = flock.Lease

// lockPath is key's lock file under a storage root.
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
	l, err := flock.Acquire(lockPath(root, key), h.line())

	return l, named(key, err)
}

// awaitPoll is how often Await tries a held lease again.
const awaitPoll = 100 * time.Millisecond

// Await takes key's lease as h, waiting while another process holds it,
// until ctx ends or wait passes; then the HeldError names the holder, beside
// ctx's error when ctx ended.
func Await(ctx context.Context, root, key string, h Holder, wait time.Duration) (*Lease, error) {
	deadline := time.Now().Add(wait)

	for {
		l, err := AcquireAs(root, key, h)
		if held := (HeldError{}); !errors.As(err, &held) || !time.Now().Before(deadline) {
			return l, err
		}

		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), err)
		case <-time.After(awaitPoll):
		}
	}
}

// Probe reports who holds key's lease, false when nobody does.
func Probe(root, key string) (Holder, bool) {
	line, held := flock.Probe(lockPath(root, key))
	if !held {
		return Holder{}, false
	}

	return parseHolder(line), true
}

// named turns flock's HeldError into one naming key and its holder.
func named(key string, err error) error {
	if held := (flock.HeldError{}); errors.As(err, &held) {
		return HeldError{Key: key, Holder: parseHolder(held.Holder)}
	}

	return err
}

// DriveKey is a drive's own lease, held for the life of its process.
func DriveKey(n int) string { return "drive-" + strconv.Itoa(n) }
