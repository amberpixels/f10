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
		t.Fatalf("Active = %+v, want drive 4 running, then drive 1 stopped", got)
	}

	for n, want := range map[int]bool{1: true, 2: false, 3: false, 4: true, 9: false} {
		if _, ok := ActiveOne(root, n); ok != want {
			t.Errorf("ActiveOne(%d) = %v, want %v", n, ok, want)
		}
	}
}
