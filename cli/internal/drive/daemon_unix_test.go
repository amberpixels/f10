package drive

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The drives these tests run: a script typed as the argv's first word, and
// a resume that names itself and ends.
const (
	waitsForDrain = `trap 'exit 6' USR1; echo 5 > "$F10_DRIVE_FILE"; echo drive 5; while :; do sleep 0.05; done`
	resumed       = `echo 5 > "$F10_DRIVE_FILE"; echo resumed; exit 0`
)

func scriptChild(argv []string) (string, []string) {
	if argv[0] == "--resume" {
		return "/bin/sh", []string{"-c", resumed}
	}

	return "/bin/sh", []string{"-c", argv[0]}
}

// shortRunDir fits a unix socket path, which t.TempDir on macOS does not.
func shortRunDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "f10f") //nolint:usetesting // a socket path has about 100 bytes
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

// testRuns serves RunService.Drive over the server with the drive's
// script as its attach key and one project, since the real handler resolves
// a project from a checkout these tests do not have.
type testRuns struct {
	f10v1connect.UnimplementedRunServiceHandler

	s *daemon.Server
}

func (r testRuns) Drive(
	_ context.Context,
	req *connect.Request[f10v1.DriveRequest],
) (*connect.Response[f10v1.DriveResponse], error) {
	m := req.Msg

	st, err := r.s.RunDrive(daemon.DriveSpec{
		Argv: m.GetArgs(), Dir: m.GetDir(), Root: "/r", Env: m.GetEnv(), Resume: int(m.GetResume()),
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&f10v1.DriveResponse{Log: st.Log, Attached: st.Attach}), nil
}

func serving(ctx context.Context, dir, version string) func(context.Context) error {
	return func(context.Context) error {
		s := &daemon.Server{
			Dir:     dir,
			Version: version,
			Child:   scriptChild,
			Key:     func(argv []string) string { return argv[0] },
			Handler: func(s *daemon.Server) http.Handler {
				_, h := f10v1connect.NewRunServiceHandler(testRuns{s: s})

				return h
			},
		}
		go func() { _ = s.Serve(ctx) }()

		return nil
	}
}

func awaitDrive(t *testing.T, dir string, n int) {
	t.Helper()

	for deadline := time.Now().Add(5 * time.Second); ; {
		st, _ := daemon.GetStatus(t.Context(), dir, "test")
		if len(st.Runs) == 1 && st.Runs[0].Drive == n {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("drive %d never ran: %+v", n, st)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func runReq(script string) daemon.DriveCall {
	return daemon.DriveCall{Args: []string{script}, Dir: "/"}
}

func TestFollowDriveEndsWithItsCode(t *testing.T) {
	dir := shortRunDir(t)

	var out syncBuffer

	err := followDrive(
		t.Context(),
		&out,
		dir,
		"0.1.0",
		"/r",
		runReq("echo drive 3; echo asks; exit 4"),
		serving(t.Context(), dir, "0.1.0"),
	)
	if testkit.ExitCode(err) != ui.ExitAsk {
		t.Fatalf("err = %v, want exit %d", err, ui.ExitAsk)
	}

	if out.String() != "drive 3\nasks\n" {
		t.Errorf("output = %q", out.String())
	}
}

// A plain stop ends the follower with the resume command, and starts no
// daemon to resume the drive.
func TestFollowDriveStopsWithAStop(t *testing.T) {
	dir := shortRunDir(t)

	var spawns atomic.Int32

	spawn := serving(t.Context(), dir, "0.1.0")
	counted := func(ctx context.Context) error { spawns.Add(1); return spawn(ctx) }

	var out syncBuffer

	done := make(chan error, 1)

	go func() { done <- followDrive(t.Context(), &out, dir, "0.1.0", "/r", runReq(waitsForDrain), counted) }()

	awaitDrive(t, dir, 5)

	if err := daemon.Stop(t.Context(), dir, true, false); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if testkit.ExitCode(err) != daemon.ExitDrained {
			t.Fatalf("err = %v, want exit %d", err, daemon.ExitDrained)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the follower did not end on a stop")
	}

	if !strings.Contains(out.String(), "f10 start --resume 5 goes on") {
		t.Errorf("output = %q", out.String())
	}

	if n := spawns.Load(); n != 1 {
		t.Errorf("%d daemons started, want only the first", n)
	}
}

// An upgrade by a newer client: the older follower never starts a daemon
// of its own, and follows its drive into the newer daemon's resume.
func TestFollowDriveFollowsAnUpgrade(t *testing.T) {
	dir := shortRunDir(t)

	var oldSpawns atomic.Int32

	first := serving(t.Context(), dir, "0.1.0")
	old := func(ctx context.Context) error { oldSpawns.Add(1); return first(ctx) }

	var out syncBuffer

	done := make(chan error, 1)

	go func() { done <- followDrive(t.Context(), &out, dir, "0.1.0", "/r", runReq(waitsForDrain), old) }()

	awaitDrive(t, dir, 5)

	newer := &daemon.Ensurer{Dir: dir, Version: "0.2.0", Spawn: serving(t.Context(), dir, "0.2.0")}

	rep, err := newer.Ensure(t.Context())
	if err != nil || rep.Version != "0.2.0" {
		t.Fatalf("upgrade = %+v, %v", rep, err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("follower err = %v\n%s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the follower did not follow the drive into the new daemon")
	}

	if !strings.Contains(out.String(), "resumed") {
		t.Errorf("output = %q", out.String())
	}

	if n := oldSpawns.Load(); n != 1 {
		t.Errorf("the older follower started %d daemons, want only the first", n)
	}

	st, err := daemon.Hello(t.Context(), dir, "test")
	if err != nil || st.Version != "0.2.0" {
		t.Errorf("daemon left running = %+v, %v", st, err)
	}
}

// A drive that exits for a resume is followed into its restarted run, and
// the follower ends with that run's code.
func TestFollowDriveFollowsAResume(t *testing.T) {
	dir := shortRunDir(t)

	var out syncBuffer

	err := followDrive(
		t.Context(),
		&out,
		dir,
		"0.1.0",
		"/r",
		runReq(`echo 5 > "$F10_DRIVE_FILE"; echo drive 5; sleep 0.3; exit 8`),
		serving(t.Context(), dir, "0.1.0"),
	)
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out.String())
	}

	if got := out.String(); !strings.Contains(got, "drive 5\n") || !strings.Contains(got, "resumed\n") {
		t.Errorf("output = %q, want both runs", got)
	}
}

// asking is a drive that stops on its asks once the daemon read its number.
const asking = `echo 5 > "$F10_DRIVE_FILE"; echo drive 5 asks; sleep 0.3; exit 4`

// waitAsks shortens the wait for a resumed run and fixes whether the
// drive's record is gone.
func waitAsks(t *testing.T, gone bool) {
	t.Helper()

	poll, was := askPoll, driveGone
	askPoll, driveGone = 10*time.Millisecond, func(string, int) bool { return gone }

	t.Cleanup(func() { askPoll, driveGone = poll, was })
}

// A drive stopped on its asks is waited on, not left: the run an answer
// resumes is followed, and the follower ends with that run's code.
func TestFollowDriveWaitsThroughAnAsk(t *testing.T) {
	waitAsks(t, false)

	dir := shortRunDir(t)

	var out syncBuffer

	done := make(chan error, 1)
	go func() {
		done <- followDrive(t.Context(), &out, dir, "0.1.0", "/r", runReq(asking), serving(t.Context(), dir, "0.1.0"))
	}()

	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(out.String(), "waits on its asks"); {
		if time.Now().After(deadline) {
			t.Fatalf("the follower never waited: %q", out.String())
		}

		time.Sleep(10 * time.Millisecond)
	}

	// the answer resumes the drive, as f10 inbox answer does
	if _, err := daemon.RunDrive(t.Context(), dir, daemon.ResumeCall("/", nil, 5)); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("err = %v\n%s", err, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the follower never followed the resumed run: %q", out.String())
	}

	if got := out.String(); !strings.Contains(got, "drive 5 asks\n") || !strings.Contains(got, "resumed\n") {
		t.Errorf("output = %q, want both runs", got)
	}
}

// A drive stopped while it waited on its asks ends the follower with the
// ask's code.
func TestFollowDriveEndsWhenAnAskingDriveStops(t *testing.T) {
	waitAsks(t, true)

	dir := shortRunDir(t)

	var out syncBuffer

	err := followDrive(t.Context(), &out, dir, "0.1.0", "/r", runReq(asking), serving(t.Context(), dir, "0.1.0"))
	if testkit.ExitCode(err) != ui.ExitAsk {
		t.Fatalf("err = %v, want exit %d\n%s", err, ui.ExitAsk, out.String())
	}

	if !strings.Contains(out.String(), "drive 5 was stopped while it waited on its asks") {
		t.Errorf("output = %q", out.String())
	}
}
