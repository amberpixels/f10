package state

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLaunchesRoundTrip(t *testing.T) {
	dir := t.TempDir()

	for _, l := range []Launch{
		{LaunchStart, "FT-1"},
		{LaunchDrive, "7"},
		{LaunchStart, "ft-1"}, // a rerun start
		{LaunchStart, "FT-2:b"},
		{LaunchDrive, "7"}, // a resumed drive
	} {
		if err := AppendLaunch(dir, "s/1", l); err != nil {
			t.Fatal(err)
		}
	}

	f, err := os.OpenFile(LaunchesPath(dir, "s/1"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = f.WriteString("garbage\nfinish FT-3\nstart \n")
	_ = f.Close()

	got, err := Launches(dir, "s/1")
	if err != nil {
		t.Fatal(err)
	}

	want := []Launch{{LaunchStart, "FT-1"}, {LaunchDrive, "7"}, {LaunchStart, "FT-2:b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("launches = %v, want %v", got, want)
	}
}

func TestLaunchesMissingOrNoSession(t *testing.T) {
	dir := t.TempDir()

	if got, err := Launches(dir, "none"); err != nil || got != nil {
		t.Fatalf("missing file = %v, %v; want none", got, err)
	}

	if err := AppendLaunch(dir, "", Launch{LaunchStart, "FT-1"}); err != nil {
		t.Fatal(err)
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("no session wrote %d files", len(entries))
	}
}

func TestPruneReclaimsLaunches(t *testing.T) {
	dir := t.TempDir()

	if err := AppendLaunch(dir, "old", Launch{LaunchStart, "FT-1"}); err != nil {
		t.Fatal(err)
	}

	if err := AppendLaunch(dir, "new", Launch{LaunchStart, "FT-2"}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	old := now.Add(-pruneAge - time.Hour)

	if err := os.Chtimes(LaunchesPath(dir, "old"), old, old); err != nil {
		t.Fatal(err)
	}

	Prune(dir, now)

	if _, err := os.Stat(LaunchesPath(dir, "old")); !os.IsNotExist(err) {
		t.Fatalf("old launches kept: %v", err)
	}

	if _, err := os.Stat(LaunchesPath(dir, "new")); err != nil {
		t.Fatalf("new launches pruned: %v", err)
	}
}
