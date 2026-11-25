package drive

import (
	"errors"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/drivestore"
)

// batchOf records a batch of ids, the ones in done already finished.
func batchOf(t *testing.T, root string, done bool, ids ...string) *drivestore.Record {
	t.Helper()

	rec := &drivestore.Record{Created: time.Now(), Args: ids}

	for _, id := range ids {
		task := drivestore.Task{ID: id}
		if done {
			drivectl.SetFinished(&task)
		}

		rec.Tasks = append(rec.Tasks, task)
	}

	if err := drivestore.Create(root, rec); err != nil {
		t.Fatal(err)
	}

	return rec
}

// A log is the newest batch's that held the task, finished or not. An older
// batch still stopped on another task is not the one the task last ran in.
func TestLogBatchIsTheNewestHoldingTheTask(t *testing.T) {
	root := t.TempDir()

	batchOf(t, root, false, "GH-1", "GH-2") // stopped, on GH-2's ask
	newer := batchOf(t, root, true, "GH-1") // finished since

	n, err := logBatch(root, "GH-1", []string{"GH-1"})
	if err != nil || n != newer.Number {
		t.Errorf("log batch = %d, %v; want the finished batch %d", n, err, newer.Number)
	}

	_, err = logBatch(root, "GH-9", []string{"GH-9"})
	if none := (NoBatchError{}); !errors.As(err, &none) || none.Token != "GH-9" {
		t.Errorf("err = %v, want a NoBatchError naming GH-9", err)
	}
}

// A batch's hints name a task that resolves back to it. Its lead no longer
// does once a newer batch holds that task.
func TestHintsNameATaskThatResolvesToTheBatch(t *testing.T) {
	root := t.TempDir()

	older := batchOf(t, root, false, "GH-1", "GH-2")
	batchOf(t, root, false, "GH-1", "GH-3")

	if got := CommandID(root, older); got != "GH-2" {
		t.Errorf("command id = %q, want GH-2: GH-1 names the newer batch", got)
	}

	in := &DriveInput{Root: root, Rec: older}
	if got := in.resumeLine(); got != "f10 resume GH-2" {
		t.Errorf("resume line = %q", got)
	}

	if got := resumeHint(root, older.Number); got != "f10 resume GH-2" {
		t.Errorf("resume hint = %q", got)
	}
}
