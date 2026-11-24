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

// recordedDrives is a checkout with two stopped batch records, GH-1's and
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

// status --batches prints the table, and with --json the records themselves.
func TestStatusBatches(t *testing.T) {
	dir, _ := recordedDrives(t)

	out, _, err := runApp(t, "-C", dir, "status", "--batches").streams()
	if err != nil || !strings.Contains(out, "GH-2") || strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("table = %q, %v; want the text table naming GH-2", out, err)
	}

	out, _, err = runApp(t, "-C", dir, "status", "--batches", "--json").streams()
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
		t.Errorf("drives = %+v, want GH-2's batch 2 first, stopped", drives)
	}

	if _, _, err := runApp(t, "-C", dir, "status", "--batches", "GH-1").streams(); err == nil {
		t.Error("status --batches took a task id")
	}
}

// f10 stop takes a task id of the batch, and refuses anything but one
// argument.
func TestStop(t *testing.T) {
	dir, root := recordedDrives(t)

	out, _, err := runApp(t, "-C", dir, "stop", "1").streams()
	if err != nil || !strings.Contains(out, "batch 1 stopped (GH-1)") {
		t.Errorf("stop 1 = %q, %v", out, err)
	}

	if rec, err := drivestore.Read(root, 1); err != nil || rec.Cancelled == nil {
		t.Errorf("batch 1 = %+v, %v; want it cancelled", rec, err)
	}

	// a task id names its batch, a stopped one too
	out, _, err = runApp(t, "-C", dir, "stop", "GH-2").streams()
	if err != nil || !strings.Contains(out, "batch 2 stopped (GH-2)") {
		t.Errorf("stop GH-2 = %q, %v", out, err)
	}

	if _, _, err := runApp(t, "-C", dir, "stop", "GH-9").streams(); err == nil ||
		!strings.Contains(err.Error(), "no batch holds GH-9") {
		t.Errorf("stop GH-9 = %v, want no batch named", err)
	}

	if _, _, err := runApp(t, "-C", dir, "stop").streams(); err == nil {
		t.Error("stop took no argument")
	}
}

// Every old spelling works for one more release and names its replacement
// on stderr, so stdout stays what it was.
func TestOldSpellingsStillWork(t *testing.T) {
	dir, root := recordedDrives(t)

	for _, tc := range []struct {
		args   []string
		out    string // stdout holds it
		notice string // stderr names the new form
	}{
		{[]string{"status", "--drives"}, "batch 2", "f10 status --batches"},
		{[]string{"stop", "1"}, "batch 1 stopped (GH-1)", "f10 stop <task-id>"},
	} {
		out, errOut, err := runApp(t, append([]string{"-C", dir}, tc.args...)...).streams()
		if err != nil || !strings.Contains(out, tc.out) {
			t.Errorf("%q = %q, %v; want it to work and print %q", tc.args, out, err, tc.out)
		}

		if !strings.Contains(errOut, "still works for one release") || !strings.Contains(errOut, tc.notice) {
			t.Errorf("%q said %q on stderr, want a notice naming %q", tc.args, errOut, tc.notice)
		}

		if strings.Contains(out, "still works") {
			t.Errorf("%q printed its notice on stdout: %q", tc.args, out)
		}
	}

	if rec, err := drivestore.Read(root, 1); err != nil || rec.Cancelled == nil {
		t.Errorf("batch 1 = %+v, %v; want stop 1 to have stopped it", rec, err)
	}

	// the two resume spellings read the number and reach the record: batch 9
	// does not exist, so each ends on that refusal, after its notice
	for _, args := range [][]string{{"resume", "9"}, {"start", "--resume", "9"}} {
		_, errOut, err := runApp(t, append([]string{"-C", dir}, args...)...).streams()
		if err == nil || !strings.Contains(errOut, "still works for one release") {
			t.Errorf("%q = %v, stderr %q; want the notice, then a refusal", args, err, errOut)
		}
	}

	// the daemon's own child passes --resume with --foreground, and its
	// stderr is the batch's log, so it gets no notice
	if _, errOut, _ := runApp(
		t,
		"-C",
		dir,
		"start",
		"--foreground",
		"--resume",
		"9",
	).streams(); strings.Contains(
		errOut,
		"still works",
	) {
		t.Errorf("the daemon's own spelling printed a notice: %q", errOut)
	}

	// the new spelling prints no notice
	if _, errOut, err := runApp(t, "-C", dir, "status", "--batches").streams(); err != nil ||
		strings.Contains(errOut, "still works") {
		t.Errorf("status --batches = %v, stderr %q; want no notice", err, errOut)
	}

	// f10 drive plan runs as f10 start --propose: this checkout has no tracker
	// to read, so both end on the same refusal, the old one after its notice
	_, oldErr, oldRes := runApp(t, "-C", dir, "drive", "plan", "GH-1", "GH-2", "--hint").streams()
	_, newErr, newRes := runApp(t, "-C", dir, "start", "GH-1", "GH-2", "--hint").streams()

	if oldRes == nil || newRes == nil || oldRes.Error() != newRes.Error() {
		t.Errorf("drive plan = %v, start --hint = %v; want one result", oldRes, newRes)
	}

	if !strings.Contains(oldErr, "f10 start <ids> --propose") || strings.Contains(newErr, "still works") {
		t.Errorf("stderr: drive plan %q, start --hint %q; want the notice on the old spelling only", oldErr, newErr)
	}
}

// f10 resume names a batch by a task, and with none takes the one stopped
// batch: several are listed, each with the command that resumes it.
func TestResumeNamesItsBatch(t *testing.T) {
	dir, root := recordedDrives(t)

	out, _, err := runApp(t, "-C", dir, "resume").streams()
	if err == nil || !strings.Contains(err.Error(), "2 batches are stopped") ||
		!strings.Contains(out, "f10 resume GH-1") || !strings.Contains(out, "f10 resume GH-2") {
		t.Errorf("a bare resume over two stopped batches = %q, %v; want both listed", out, err)
	}

	if _, _, err := runApp(t, "-C", dir, "resume", "GH-9").streams(); err == nil ||
		!strings.Contains(err.Error(), "no batch to resume holds GH-9") {
		t.Errorf("resume GH-9 = %v, want no batch named", err)
	}

	if _, _, err := runApp(t, "-C", dir, "resume", "GH-1", "--flow", "fast").streams(); err == nil ||
		!strings.Contains(err.Error(), "it takes only") {
		t.Errorf("resume --flow = %v, want the flag refused", err)
	}

	now := time.Now()
	for _, n := range []int{1, 2} {
		if _, err := drivestore.Update(root, n, func(r *drivestore.Record) { r.Cancelled = &now }); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := runApp(t, "-C", dir, "resume").streams(); err == nil ||
		!strings.Contains(err.Error(), "no stopped batch in this project") {
		t.Errorf("a bare resume with nothing stopped = %v", err)
	}

	// f10 stop ends a batch for good, and a resume by its task says so
	if _, _, err := runApp(t, "-C", dir, "resume", "GH-1").streams(); err == nil ||
		!strings.Contains(err.Error(), "batch 1 was stopped for good") {
		t.Errorf("resume GH-1 after its stop = %v, want the batch named as stopped for good", err)
	}

	// a tracker prefix alone names no batch
	if _, _, err := runApp(t, "-C", dir, "stop", "GH").streams(); err == nil {
		t.Error("stop GH took a bare prefix as a task")
	}
}

// f10 daemon logs names a batch by a task, and still reads a number as one
// for one more release, with its notice.
func TestDaemonLogsNamesItsBatchByATask(t *testing.T) {
	dir, _ := recordedDrives(t)

	_, _, err := runApp(t, "-C", dir, "daemon", "logs", "GH-2").streams()
	if err == nil || !strings.Contains(err.Error(), "no daemon log for batch 2") {
		t.Errorf("logs GH-2 = %v, want the task resolved to batch 2, which has no log here", err)
	}

	if _, _, err := runApp(t, "-C", dir, "daemon", "logs", "GH-9").streams(); err == nil ||
		!strings.Contains(err.Error(), "no batch holds GH-9") {
		t.Errorf("logs GH-9 = %v, want no batch named", err)
	}

	_, errOut, err := runApp(t, "-C", dir, "daemon", "logs", "1").streams()
	if err == nil || !strings.Contains(err.Error(), "no daemon log for batch 1") ||
		!strings.Contains(errOut, "f10 daemon logs <task-id>") {
		t.Errorf("logs 1 = %v, stderr %q; want batch 1 read and the notice", err, errOut)
	}
}
