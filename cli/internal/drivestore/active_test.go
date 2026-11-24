package drivestore

import (
	"testing"
	"time"
)

func TestActiveSkipsCancelledAndDoneAndProbesTheDriver(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Now()

	for _, rec := range []*Record{
		{Tasks: []Task{{ID: "FT-1"}}},
		{Cancelled: &now, Tasks: []Task{{ID: "FT-2"}}},
		{Tasks: []Task{{ID: "FT-3", Outcome: &Outcome{Row: "ok"}}}},
		{Tasks: []Task{{ID: "FT-4"}}},
	} {
		if err := Create(root, rec); err != nil {
			t.Fatal(err)
		}
	}

	lease, err := Acquire(root, DriveKey(4), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	got := Active(root)
	if len(got) != 2 || got[0].Number != 4 || !got[0].Running || got[1].Number != 1 || got[1].Running {
		t.Fatalf("Active = %+v, want batch 4 running, then batch 1 stopped", got)
	}

	for n, want := range map[int]bool{1: true, 2: false, 3: false, 4: true, 9: false} {
		if _, ok := ActiveOne(root, n); ok != want {
			t.Errorf("ActiveOne(%d) = %v, want %v", n, ok, want)
		}
	}
}

// A batch is named by any task it holds: a bundle's rider, a task typed
// with a suffix, in any case, and never by a longer id sharing a prefix.
func TestHoldingNamesABatchByAnyTask(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Now()

	for _, rec := range []*Record{
		{Tasks: []Task{{ID: "FT-1+FT-2"}, {ID: "FT-3-retry"}}},
		{Cancelled: &now, Tasks: []Task{{ID: "FT-4"}}},
		{Tasks: []Task{{ID: "FT-2"}, {ID: "FT-12"}}},
	} {
		if err := Create(root, rec); err != nil {
			t.Fatal(err)
		}
	}

	for id, want := range map[string]int{"FT-1": 1, "ft-2": 3, "FT-3": 1, "FT-3-retry": 1, "FT-12": 3, "FT-4": 0, "FT-9": 0, "": 0} {
		l, ok := Holding(root, id)
		if got := l.Record; (got == nil) != (want == 0) || ok != (want != 0) || (got != nil && got.Number != want) {
			t.Errorf("Holding(%q) = %+v, %v; want batch %d", id, got, ok, want)
		}
	}

	lease, err := Acquire(root, DriveKey(3), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	stopped := Stopped(root)
	if len(stopped) != 1 || stopped[0].Number != 1 || stopped[0].Lead() != "FT-1" {
		t.Errorf("Stopped = %+v, want only batch 1, led by FT-1", stopped)
	}
}
