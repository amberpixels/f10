//go:build unix

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// recordedDrives is a checkout with two stopped drive records, GH-1's and
// GH-2's, and its storage root.
func recordedDrives(t *testing.T) (string, string) {
	t.Helper()

	dir := testkit.PrRepo(t)

	tg, err := target.At(t.Context(), dir, true, "test")
	if err != nil {
		t.Fatal(err)
	}

	root := tg.Layout.StorageRoot.Path()

	for _, id := range []string{"GH-1", "GH-2"} {
		rec := &drivestore.Record{Created: time.Now(), Args: []string{id}, Tasks: []drivestore.Task{{ID: id}}}
		if err := drivestore.Create(root, rec); err != nil {
			t.Fatal(err)
		}
	}

	return dir, root
}

// status --drives prints the table, and with --json the records themselves.
func TestStatusDrives(t *testing.T) {
	dir, _ := recordedDrives(t)

	out, _, err := runApp(t, "-C", dir, "status", "--drives").streams()
	if err != nil || !strings.Contains(out, "GH-2") || strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("table = %q, %v; want the text table naming GH-2", out, err)
	}

	out, _, err = runApp(t, "-C", dir, "status", "--drives", "--json").streams()
	if err != nil {
		t.Fatal(err)
	}

	var drives []struct {
		Number int      `json:"number"`
		Args   []string `json:"args"`
		State  string   `json:"state"`
	}

	if err := json.Unmarshal([]byte(out), &drives); err != nil {
		t.Fatalf("--json = %q: %v", out, err)
	}

	if len(drives) != 2 || drives[0].Number != 2 || drives[0].State != "stopped" ||
		strings.Join(drives[0].Args, " ") != "GH-2" {
		t.Errorf("drives = %+v, want GH-2's drive 2 first, stopped", drives)
	}

	if _, _, err := runApp(t, "-C", dir, "status", "--drives", "GH-1").streams(); err == nil {
		t.Error("status --drives took a task id")
	}
}

// f10 stop takes a drive number, and refuses anything but one argument.
func TestStop(t *testing.T) {
	dir, root := recordedDrives(t)

	out, _, err := runApp(t, "-C", dir, "stop", "1").streams()
	if err != nil || !strings.Contains(out, "drive 1 stopped (GH-1)") {
		t.Errorf("stop 1 = %q, %v", out, err)
	}

	if rec, err := drivestore.Read(root, 1); err != nil || rec.Cancelled == nil {
		t.Errorf("drive 1 = %+v, %v; want it cancelled", rec, err)
	}

	if _, _, err := runApp(t, "-C", dir, "stop", "GH-2").streams(); err == nil ||
		!strings.Contains(err.Error(), "no running drive holds GH-2") {
		t.Errorf("stop GH-2 = %v, want no running drive named", err)
	}

	if _, _, err := runApp(t, "-C", dir, "stop").streams(); err == nil {
		t.Error("stop took no argument")
	}
}
