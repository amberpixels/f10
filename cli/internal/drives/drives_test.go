//go:build unix

package drives

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestLeaseIsExclusive(t *testing.T) {
	root := t.TempDir()

	l, err := Acquire(root, "GH-1", 3)
	if err != nil {
		t.Fatal(err)
	}

	// flock conflicts per open file, so a second open in this process counts
	_, err = Acquire(root, "GH-1", 4)

	var held HeldError
	if !errors.As(err, &held) {
		t.Fatalf("second Acquire = %v, want HeldError", err)
	}

	if want := (Holder{PID: os.Getpid(), Drive: 3}); held.Holder != want {
		t.Errorf("holder = %+v, want %+v", held.Holder, want)
	}

	if got := err.Error(); got != "GH-1 is owned by drive 3 (pid "+strconv.Itoa(os.Getpid())+")" {
		t.Errorf("error = %q", got)
	}

	if h, ok := Probe(root, "GH-1"); !ok || h.Drive != 3 {
		t.Errorf("Probe while held = %+v, %v", h, ok)
	}

	l.Release()
	l.Release() // a second release is fine

	if _, ok := Probe(root, "GH-1"); ok {
		t.Error("Probe after release reads held")
	}

	l2, err := Acquire(root, "GH-1", 0)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	defer l2.Release()

	_, err = Acquire(root, "GH-1", 5)
	if !errors.As(err, &held) || held.Holder.Drive != 0 ||
		held.Holder.String() != "f10 finish (pid "+strconv.Itoa(os.Getpid())+")" {
		t.Errorf("finish's lease reads %v", err)
	}
}

func TestProbeOnNoLockFile(t *testing.T) {
	if _, ok := Probe(t.TempDir(), "GH-9"); ok {
		t.Error("a lease never taken reads held")
	}
}

// A lock file a dead process left reads free: the kernel dropped its lock.
func TestStaleLockFileReadsFree(t *testing.T) {
	root := t.TempDir()

	l, err := Acquire(root, "GH-2", 7)
	if err != nil {
		t.Fatal(err)
	}

	l.Release()

	if _, ok := Probe(root, "GH-2"); ok {
		t.Error("a released lock file reads held")
	}

	if _, err := Acquire(root, "GH-2", 8); err != nil {
		t.Errorf("Acquire over a stale file: %v", err)
	}
}

func TestCreateNumbersRecords(t *testing.T) {
	root := t.TempDir()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[int]bool{}
	)

	for range 8 {
		wg.Go(func() {
			rec := &Record{Args: []string{"GH-1"}}
			if err := Create(root, rec); err != nil {
				t.Error(err)

				return
			}

			mu.Lock()
			defer mu.Unlock()

			if seen[rec.Number] {
				t.Errorf("number %d given twice", rec.Number)
			}

			seen[rec.Number] = true
		})
	}

	wg.Wait()

	if len(seen) != 8 {
		t.Errorf("got %d numbers, want 8", len(seen))
	}
}

func TestUpdateKeepsAConcurrentCancel(t *testing.T) {
	root := t.TempDir()

	rec := &Record{Args: []string{"GH-1"}, Tasks: []Task{{ID: "GH-1"}}}
	if err := Create(root, rec); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	wg.Go(func() {
		for range 20 {
			if _, err := Update(root, rec.Number, func(r *Record) { r.Task("GH-1").Skill = "ship" }); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Go(func() {
		now := time.Now()
		if _, err := Update(root, rec.Number, func(r *Record) { r.Cancelled = &now }); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()

	got, err := Read(root, rec.Number)
	if err != nil {
		t.Fatal(err)
	}

	if got.Cancelled == nil || got.Task("GH-1").Skill != "ship" {
		t.Errorf("record = %+v, want cancelled and at ship", got)
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()

	if r, err := Find(root, []string{"GH-1"}); err != nil || r != nil {
		t.Fatalf("Find with no records = %v, %v", r, err)
	}

	now := time.Now()
	done := &Record{Args: []string{"GH-1"}, Tasks: []Task{{ID: "GH-1", Outcome: &Outcome{Row: "finished"}}}}
	open := &Record{Args: []string{"GH-1"}, Tasks: []Task{{ID: "GH-1", Outcome: &Outcome{Row: "halted", Code: 5}}}}
	cancelled := &Record{Args: []string{"GH-1"}, Tasks: []Task{{ID: "GH-1"}}, Cancelled: &now}

	for _, r := range []*Record{open, done, cancelled} {
		if err := Create(root, r); err != nil {
			t.Fatal(err)
		}
	}

	r, err := Find(root, []string{"gh-1"})
	if err != nil || r == nil || r.Number != open.Number {
		t.Errorf("Find = %+v, %v, want record %d", r, err, open.Number)
	}

	if r, _ := Find(root, []string{"GH-1", "GH-2"}); r != nil {
		t.Errorf("Find on other args = record %d", r.Number)
	}

	recs, err := List(root)
	if err != nil || len(recs) != 3 || recs[0].Number != cancelled.Number {
		t.Errorf("List = %d records, %v; newest first", len(recs), err)
	}
}

// The creating session round-trips; a record written before the field
// existed reads as none.
func TestRecordKeepsItsSession(t *testing.T) {
	root := t.TempDir()

	for _, session := range []string{"sess-1", ""} {
		rec := &Record{Args: []string{"GH-1", session}, Session: session}
		if err := Create(root, rec); err != nil {
			t.Fatal(err)
		}

		got, err := Read(root, rec.Number)
		if err != nil {
			t.Fatal(err)
		}

		if got.Session != session {
			t.Errorf("record %d session = %q, want %q", rec.Number, got.Session, session)
		}
	}
}
