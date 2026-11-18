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

// f10 stop takes a drive number or a task a running drive holds, and names
// the task no drive holds.
func TestCancelDrive(t *testing.T) {
	dir, root, c := drivesProject(t, 2)

	if _, err := c.CancelDrive(t.Context(), connect.NewRequest(&f10v1.CancelDriveRequest{
		Project: dir, Drive: 2,
	})); err != nil {
		t.Fatal(err)
	}

	if rec, err := drivestore.Read(root, 2); err != nil || rec.Cancelled == nil {
		t.Errorf("drive 2 = %+v, %v; want it cancelled", rec, err)
	}

	res, err := c.CancelDrive(t.Context(), connect.NewRequest(&f10v1.CancelDriveRequest{Project: dir, Task: "GH-1"}))
	if err != nil {
		t.Fatal(err)
	}

	if rec, _ := drivestore.Read(root, 1); rec == nil || rec.Cancelled == nil ||
		!strings.Contains(res.Msg.GetPrinted().GetStdout(), "drive 1 stopped") {
		t.Errorf("GH-1's drive = %+v, printed %q; want drive 1 cancelled", rec, res.Msg.GetPrinted().GetStdout())
	}

	_, err = c.CancelDrive(t.Context(), connect.NewRequest(&f10v1.CancelDriveRequest{Project: dir, Task: "GH-9"}))
	if connect.CodeOf(err) != connect.CodeNotFound || !strings.Contains(err.Error(), "no running drive holds GH-9") {
		t.Errorf("an unheld task = %v, want NotFound naming it", err)
	}

	_, err = c.CancelDrive(t.Context(), connect.NewRequest(&f10v1.CancelDriveRequest{Project: dir}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("no drive or task = %v, want InvalidArgument", err)
	}
}

// status --drives lists the newest records the table shows, and every one
// with all.
func TestListDrives(t *testing.T) {
	dir, _, c := drivesProject(t, drive.DrivesShown+1)

	res, err := c.ListDrives(t.Context(), connect.NewRequest(&f10v1.ListDrivesRequest{Project: dir}))
	if err != nil {
		t.Fatal(err)
	}

	drives := res.Msg.GetDrives()
	if len(drives) != drive.DrivesShown || drives[0].GetNumber() != int32(drive.DrivesShown+1) {
		t.Errorf("drives = %d, newest %d; want the newest %d", len(drives), drives[0].GetNumber(), drive.DrivesShown)
	}

	res, err = c.ListDrives(t.Context(), connect.NewRequest(&f10v1.ListDrivesRequest{Project: dir, All: true}))
	if err != nil || len(res.Msg.GetDrives()) != drive.DrivesShown+1 {
		t.Errorf("all = %d drives, %v; want every one", len(res.Msg.GetDrives()), err)
	}
}

// Drive's field form never ran start's closed check, so a start of closed
// tasks is refused naming Start, which opens the discussion.
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
