//go:build unix

package api

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivestore"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// drivesProject is a checkout with n drive records, the newest holding the
// GH-1 lease, and a client for its API; the project is the checkout's path.
func drivesProject(t *testing.T, n int) (string, string, f10v1connect.RunServiceClient) {
	t.Helper()

	dir := testkit.PrRepo(t)

	tg, err := target.At(t.Context(), dir, true, "test")
	if err != nil {
		t.Fatal(err)
	}

	root := tg.Layout.StorageRoot.Path()

	for i := range n {
		id := "GH-" + strconv.Itoa(i+1)
		rec := &drivestore.Record{Created: time.Now(), Args: []string{id}, Tasks: []drivestore.Task{{ID: id}}}

		if err := drivestore.Create(root, rec); err != nil {
			t.Fatal(err)
		}
	}

	l, err := drivestore.Acquire(root, "GH-1", 1)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(l.Release)

	srv := httptest.NewServer(Handler(&fakeSup{}))
	t.Cleanup(srv.Close)

	return dir, root, f10v1connect.NewRunServiceClient(srv.Client(), srv.URL)
}

// CancelDrive is StopBatch's old name: it still takes a batch number, or a
// task, and names the task no batch holds.
//
//nolint:staticcheck // the old name, still served
func TestCancelDrive(t *testing.T) {
	dir, root, c := drivesProject(t, 2)

	if _, err := c.CancelDrive(t.Context(), connect.NewRequest(&f10v1.CancelDriveRequest{
		Project: dir, Drive: 2,
	})); err != nil {
		t.Fatal(err)
	}

	if rec, err := drivestore.Read(root, 2); err != nil || rec.Cancelled == nil {
		t.Errorf("batch 2 = %+v, %v; want it cancelled", rec, err)
	}

	res, err := c.CancelDrive(t.Context(), connect.NewRequest(&f10v1.CancelDriveRequest{Project: dir, Task: "GH-1"}))
	if err != nil {
		t.Fatal(err)
	}

	if rec, _ := drivestore.Read(root, 1); rec == nil || rec.Cancelled == nil ||
		!strings.Contains(res.Msg.GetPrinted().GetStdout(), "batch 1 stopped") {
		t.Errorf("GH-1's drive = %+v, printed %q; want batch 1 cancelled", rec, res.Msg.GetPrinted().GetStdout())
	}

	_, err = c.CancelDrive(t.Context(), connect.NewRequest(&f10v1.CancelDriveRequest{Project: dir, Task: "GH-9"}))
	if connect.CodeOf(err) != connect.CodeNotFound || !strings.Contains(err.Error(), "no batch holds GH-9") {
		t.Errorf("an unheld task = %v, want NotFound naming it", err)
	}

	_, err = c.CancelDrive(t.Context(), connect.NewRequest(&f10v1.CancelDriveRequest{Project: dir}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("no drive or task = %v, want InvalidArgument", err)
	}
}

// status --batches lists the newest records the table shows, and every one
// with all.
func TestListBatches(t *testing.T) {
	dir, _, c := drivesProject(t, drive.DrivesShown+1)

	res, err := c.ListBatches(t.Context(), connect.NewRequest(&f10v1.ListBatchesRequest{Project: dir}))
	if err != nil {
		t.Fatal(err)
	}

	batches := res.Msg.GetBatches()
	if len(batches) != drive.DrivesShown || batches[0].GetNumber() != int32(drive.DrivesShown+1) {
		t.Errorf("batches = %d, newest %d; want the newest %d", len(batches), batches[0].GetNumber(), drive.DrivesShown)
	}

	res, err = c.ListBatches(t.Context(), connect.NewRequest(&f10v1.ListBatchesRequest{Project: dir, All: true}))
	if err != nil || len(res.Msg.GetBatches()) != drive.DrivesShown+1 {
		t.Errorf("all = %d batches, %v; want every one", len(res.Msg.GetBatches()), err)
	}
}

// Drive's field form never ran start's closed check, so a start of closed
// tasks is refused naming Start, which opens the discussion.
//
//nolint:staticcheck // the old name, whose field form only it takes
func TestDriveFieldsRefuseClosedTasks(t *testing.T) {
	seam.Swap(t, &start.ClosedStart, func(_ context.Context, _ *target.Target, ids []string) []string { return ids })

	sup := &fakeSup{}
	srv := httptest.NewServer(Handler(sup))
	t.Cleanup(srv.Close)

	c := f10v1connect.NewRunServiceClient(srv.Client(), srv.URL)
	dir := testkit.PrRepo(t)

	_, err := c.Drive(t.Context(), connect.NewRequest(&f10v1.DriveRequest{
		Dir: dir, Tasks: []*f10v1.DriveTask{{Id: "GH-1"}},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "Start opens") {
		t.Errorf("err = %v, want FailedPrecondition naming Start", err)
	}

	// typed arguments come from f10 start, which already checked
	if _, err := c.Drive(t.Context(), connect.NewRequest(&f10v1.DriveRequest{
		Dir: dir, Args: []string{"GH-1"},
	})); err != nil || len(sup.drives) != 1 {
		t.Errorf("typed args = %v, ran %d drives; want the drive run", err, len(sup.drives))
	}
}

// Each batch method answers as its old name does, so the two never drift:
// the list holds the same records, and a stop by task stops the same batch.
//
//nolint:staticcheck // the old names, compared with their replacements
func TestBatchMethodsMatchTheirOldNames(t *testing.T) {
	dir, root, c := drivesProject(t, 3)

	old, err := c.ListDrives(t.Context(), connect.NewRequest(&f10v1.ListDrivesRequest{Project: dir}))
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.ListBatches(t.Context(), connect.NewRequest(&f10v1.ListBatchesRequest{Project: dir}))
	if err != nil {
		t.Fatal(err)
	}

	batches := res.Msg.GetBatches()
	if len(batches) != 3 || len(old.Msg.GetDrives()) != 3 ||
		res.Msg.GetPrinted().GetStdout() != old.Msg.GetPrinted().GetStdout() {
		t.Fatalf("batches = %+v, drives = %+v; want the same three", batches, old.Msg.GetDrives())
	}

	for i, b := range batches {
		d := old.Msg.GetDrives()[i]
		if b.GetNumber() != d.GetNumber() || b.GetState() != d.GetState() ||
			strings.Join(b.GetArgs(), " ") != strings.Join(d.GetArgs(), " ") {
			t.Errorf("batch %+v differs from its old form %+v", b, d)
		}
	}

	if got := strings.Join(batches[0].GetTasks(), " "); got != "GH-3" {
		t.Errorf("the newest batch's tasks = %q, want GH-3", got)
	}

	// GH-1's batch runs, GH-3's is stopped: a task names either
	for task, n := range map[string]int{"GH-1": 1, "GH-3": 3} {
		out, err := c.StopBatch(t.Context(), connect.NewRequest(&f10v1.StopBatchRequest{Project: dir, Task: task}))
		if err != nil {
			t.Fatal(err)
		}

		if rec, _ := drivestore.Read(root, n); rec == nil || rec.Cancelled == nil ||
			!strings.Contains(out.Msg.GetPrinted().GetStdout(), "batch "+strconv.Itoa(n)+" stopped") {
			t.Errorf("stop %s left batch %d = %+v, printed %q", task, n, rec, out.Msg.GetPrinted().GetStdout())
		}
	}

	_, err = c.StopBatch(t.Context(), connect.NewRequest(&f10v1.StopBatchRequest{Project: dir}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a stop with no task = %v, want InvalidArgument", err)
	}
}

// Resume names a batch by a task and hands the supervisor the record's
// --resume line, with the answers starting a process of their own.
func TestResumeByTask(t *testing.T) {
	sup := &fakeSup{}
	srv := httptest.NewServer(Handler(sup))
	t.Cleanup(srv.Close)

	c := f10v1connect.NewRunServiceClient(srv.Client(), srv.URL)
	dir := testkit.PrRepo(t)

	tg, err := target.At(t.Context(), dir, true, "test")
	if err != nil {
		t.Fatal(err)
	}

	root := tg.Layout.StorageRoot.Path()

	_, err = c.Resume(t.Context(), connect.NewRequest(&f10v1.ResumeRequest{Project: dir}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a resume with nothing stopped = %v, want NotFound", err)
	}

	for _, ids := range []string{"GH-1+GH-2", "GH-3"} {
		rec := &drivestore.Record{Created: time.Now(), Args: []string{ids}, Tasks: []drivestore.Task{{ID: ids}}}
		if err := drivestore.Create(root, rec); err != nil {
			t.Fatal(err)
		}
	}

	_, err = c.Resume(t.Context(), connect.NewRequest(&f10v1.ResumeRequest{Project: dir}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "GH-1") ||
		!strings.Contains(err.Error(), "GH-3") {
		t.Errorf("a resume over two stopped batches = %v, want both named", err)
	}

	// a rider names its bundle's batch
	res, err := c.Resume(t.Context(), connect.NewRequest(&f10v1.ResumeRequest{Project: dir, Task: "GH-2"}))
	if err != nil {
		t.Fatal(err)
	}

	if res.Msg.GetBatch() != 1 || len(sup.drives) != 1 || sup.drives[0].Resume != 1 ||
		strings.Join(sup.drives[0].Argv, " ") != "--resume 1" ||
		!strings.Contains(res.Msg.GetPrinted().GetStdout(), "batch 1 resumed: f10 daemon logs GH-1 -f") {
		t.Errorf("resume GH-2 = %+v, ran %+v", res.Msg, sup.drives)
	}

	_, err = c.Resume(t.Context(), connect.NewRequest(&f10v1.ResumeRequest{
		Project: dir, Task: "GH-3", Answers: []string{"1. yes"}, Parallel: 2,
	}))
	if err != nil {
		t.Fatal(err)
	}

	if got := sup.drives[1]; got.Resume != 0 ||
		strings.Join(got.Argv, "|") != "--resume|2|--answer|1. yes|--parallel|2" {
		t.Errorf("a resume with answers ran %+v, want its own process and the flags on the line", got)
	}

	_, err = c.Resume(t.Context(), connect.NewRequest(&f10v1.ResumeRequest{Project: dir, Task: "GH-9"}))
	if connect.CodeOf(err) != connect.CodeNotFound || !strings.Contains(err.Error(), "GH-9") {
		t.Errorf("resume GH-9 = %v, want NotFound naming it", err)
	}
}
