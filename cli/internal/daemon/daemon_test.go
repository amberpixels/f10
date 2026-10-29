//go:build unix

package daemon

import (
	"context"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runDir is a short dir: a unix socket path has about 100 bytes, and the
// test temp dir's path on macOS uses most of them.
func runDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "f10d") //nolint:usetesting // t.TempDir's path is too long for a socket
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}

// shell runs a drive's first argument as a shell script.
func shell(argv []string) (string, []string) { return "/bin/sh", []string{"-c", argv[0]} }

func serve(t *testing.T, s *Server) <-chan error {
	t.Helper()

	done := make(chan error, 1)

	go func() { done <- s.Serve(t.Context()) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := Call(t.Context(), s.Dir, Request{Op: OpHello}, 0); err == nil {
			return done
		}

		if time.Now().After(deadline) {
			t.Fatal("the daemon did not listen")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func waitExit(t *testing.T, log string) Exit {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if exit, ok := ReadExit(log); ok {
			return exit
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s never exited", log)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestServeRunsAndAttaches(t *testing.T) {
	s := &Server{Dir: runDir(t), Version: "v1", Child: shell}
	serve(t, s)

	req := Request{
		Op:   OpRun,
		Argv: []string{`echo 3 > "$F10_DRIVE_FILE"; echo drive 3; sleep 0.3; echo done; exit 4`},
		Dir:  "/",
		Root: "/r",
		Key:  "GH-1",
	}

	first, err := Call(t.Context(), s.Dir, req, 0)
	if err != nil {
		t.Fatal(err)
	}

	second, err := Call(t.Context(), s.Dir, req, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !second.Attach || second.Log != first.Log {
		t.Errorf("second client got %+v, want to attach to %s", second, first.Log)
	}

	other := req
	other.Root = "/other"

	if rep, err := Call(t.Context(), s.Dir, other, 0); err != nil || rep.Attach {
		t.Errorf("another project's drive attached: %+v, %v", rep, err)
	}

	if exit := waitExit(t, first.Log); exit != (Exit{Code: 4, Drive: 3}) {
		t.Errorf("exit = %+v, want 4 of drive 3", exit)
	}

	raw, _ := os.ReadFile(first.Log)
	if string(raw) != "drive 3\ndone\n" {
		t.Errorf("log = %q", raw)
	}

	if got := FindLog(s.Dir, "/r", 3); got != first.Log {
		t.Errorf("FindLog = %q, want %q", got, first.Log)
	}
}

func TestServeExitsWhenIdle(t *testing.T) {
	s := &Server{Dir: runDir(t), Version: "v1", Child: shell, Idle: 50 * time.Millisecond, Tick: 10 * time.Millisecond}
	done := serve(t, s)

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an idle daemon did not exit")
	}

	if _, err := os.Stat(SocketPath(s.Dir)); !os.IsNotExist(err) {
		t.Errorf("socket left behind: %v", err)
	}
}

// A drain stops each drive at its tick (exit 6), and the next daemon
// resumes it by its record number with the same dir and environment.
func TestDrainResumesInTheNextDaemon(t *testing.T) {
	dir := runDir(t)

	s := &Server{Dir: dir, Version: "v1", Child: shell}
	done := serve(t, s)

	script := `trap 'exit 6' USR1; echo 5 > "$F10_DRIVE_FILE"; while :; do sleep 0.05; done`

	rep, err := Call(
		t.Context(),
		dir,
		Request{Op: OpRun, Argv: []string{script}, Dir: "/", Root: "/r", Env: []string{"A=1"}},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}

	for deadline := time.Now().Add(5 * time.Second); ; {
		st, _ := Call(t.Context(), dir, Request{Op: OpStatus}, 0)
		if len(st.Runs) == 1 && st.Runs[0].Drive == 5 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("the drive never named itself: %+v", st)
		}

		time.Sleep(10 * time.Millisecond)
	}

	if _, err := Call(t.Context(), dir, Request{Op: OpStop, Drain: true, Upgrade: true}, 0); err != nil {
		t.Fatal(err)
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if exit := waitExit(t, rep.Log); exit != (Exit{Code: ExitDrained, Drive: 5, Reason: ReasonUpgrade}) {
		t.Fatalf("drained drive exited %+v", exit)
	}

	var (
		mu  sync.Mutex
		got [][]string
	)

	next := &Server{Dir: dir, Version: "v2", Child: func(argv []string) (string, []string) {
		mu.Lock()
		got = append(got, argv)
		mu.Unlock()

		return "/bin/sh", []string{"-c", `echo 5 > "$F10_DRIVE_FILE"; echo "$A"`}
	}}
	serve(t, next)

	mu.Lock()
	defer mu.Unlock()

	if len(got) != 1 || !slices.Equal(got[0], []string{"--resume", "5"}) {
		t.Errorf("the next daemon ran %v, want one --resume 5", got)
	}

	if _, err := os.Stat(drainedPath(dir)); !os.IsNotExist(err) {
		t.Errorf("drained list left after the resume: %v", err)
	}
}

func TestEnsureStartsOneDaemon(t *testing.T) {
	dir := runDir(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var spawns atomic.Int32

	ensurer := func(version string) *Ensurer {
		return &Ensurer{Dir: dir, Version: version, Spawn: func(context.Context) error {
			spawns.Add(1)

			go func() { _ = (&Server{Dir: dir, Version: version, Child: shell}).Serve(ctx) }()

			return nil
		}}
	}

	var wg sync.WaitGroup

	errs := make(chan error, 2)

	for range 2 {
		wg.Go(func() {
			_, err := ensurer("0.1.0").Ensure(ctx)
			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	if n := spawns.Load(); n != 1 {
		t.Errorf("two clients started %d daemons", n)
	}

	// a newer client drains the old daemon and starts its own
	var drained string

	e := ensurer("0.2.0")
	e.Draining = func(old string) { drained = old }

	rep, err := e.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if rep.Version != "0.2.0" || drained != "0.1.0" || spawns.Load() != 2 {
		t.Errorf("replaced to %+v, drained %q, spawns %d", rep, drained, spawns.Load())
	}
}

// A plain stop drains too, but hands nothing to a next daemon: the drive's
// exit says it stopped, and the next daemon resumes nothing.
func TestStopKeepsNoDrainedList(t *testing.T) {
	dir := runDir(t)

	s := &Server{Dir: dir, Version: "v1", Child: shell}
	done := serve(t, s)

	script := `trap 'exit 6' USR1; echo 5 > "$F10_DRIVE_FILE"; while :; do sleep 0.05; done`

	rep, err := Call(t.Context(), dir, Request{Op: OpRun, Argv: []string{script}, Dir: "/", Root: "/r"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	waitNumbered(t, dir, 5)

	if _, err := Call(t.Context(), dir, Request{Op: OpStop, Drain: true}, 0); err != nil {
		t.Fatal(err)
	}

	<-done

	if exit := waitExit(t, rep.Log); exit.Reason != ReasonStopped || exit.Drive != 5 {
		t.Errorf("stopped drive exited %+v", exit)
	}

	if _, err := os.Stat(drainedPath(dir)); !os.IsNotExist(err) {
		t.Errorf("a plain stop left a drained list: %v", err)
	}
}

// A drive that ignores the drain keeps the socket up, so a stop --now can
// end it rather than waiting out DrainWait.
func TestStopNowCutsADrainShort(t *testing.T) {
	dir := runDir(t)

	s := &Server{Dir: dir, Version: "v1", Child: shell, DrainWait: time.Hour}
	done := serve(t, s)

	if _, err := Call(
		t.Context(),
		dir,
		Request{
			Op:   OpRun,
			Argv: []string{`trap '' USR1; echo 2 > "$F10_DRIVE_FILE"; while :; do sleep 0.05; done`},
			Dir:  "/",
			Root: "/r",
		},
		0,
	); err != nil {
		t.Fatal(err)
	}

	waitNumbered(t, dir, 2)

	if _, err := Call(t.Context(), dir, Request{Op: OpStop, Drain: true}, 0); err != nil {
		t.Fatal(err)
	}

	st, err := Call(t.Context(), dir, Request{Op: OpStatus}, 0)
	if err != nil || !st.Stopping {
		t.Fatalf("a draining daemon's status = %+v, %v", st, err)
	}

	if _, err := Call(t.Context(), dir, Request{Op: OpStop}, 0); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop --now did not end the drain")
	}
}

// An older client, or one of another development build, uses the daemon
// running rather than replacing it; following, any version is used.
func TestEnsureReplacesOnlyForANewerRelease(t *testing.T) {
	dir := runDir(t)

	serve(t, &Server{Dir: dir, Version: "0.46.0", Child: shell})

	spawned := false

	for _, e := range []*Ensurer{
		{Dir: dir, Version: "0.45.0"},
		{Dir: dir, Version: "devel abc"},
		{Dir: dir, Version: "0.40.0", Follow: true},
	} {
		var kept string

		e.Kept = func(running string) { kept = running }
		e.Spawn = func(context.Context) error { spawned = true; return nil }

		rep, err := e.Ensure(t.Context())
		if err != nil || rep.Version != "0.46.0" {
			t.Errorf("%s: ensure = %+v, %v", e.Version, rep, err)
		}

		if want := map[bool]string{true: "", false: "0.46.0"}[e.Follow]; kept != want {
			t.Errorf("%s: kept = %q, want %q", e.Version, kept, want)
		}
	}

	if spawned {
		t.Error("an older or unordered client started a daemon")
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0.46.0", "0.45.9", true},
		{"v1.0.0", "0.99.0", true},
		{"0.45.0", "0.46.0", false},
		{"0.45.0", "0.45.0", false},
		{"devel abc", "0.45.0", false},
		{"0.45.0", "devel abc", false},
	} {
		if got := Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestPruneLogsKeepsRunningAndRecent(t *testing.T) {
	dir := runDir(t)
	logs := logsDir(dir)

	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}

	old, recent, running := logs+"/old.log", logs+"/recent.log", logs+"/running.log"
	for _, p := range []string{old, recent, running} {
		_ = os.WriteFile(p, nil, 0o600)
	}

	_ = writeExit(old, Exit{Code: 0, Drive: 1})
	_ = writeExit(recent, Exit{Code: 0, Drive: 2})

	past := time.Now().Add(-2 * logAge)
	_ = os.Chtimes(old, past, past)
	_ = os.Chtimes(running, past, past)
	_ = os.WriteFile(logs+"/index", []byte("/r\t1\t"+old+"\n/r\t2\t"+recent+"\n"), 0o600)

	pruneLogs(dir, time.Now())

	for p, want := range map[string]bool{old: false, recent: true, running: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s kept = %v, want %v", p, err == nil, want)
		}
	}

	if FindLog(dir, "/r", 1) != "" || FindLog(dir, "/r", 2) != recent {
		t.Error("the index still names the pruned log, or lost the kept one")
	}
}

func waitNumbered(t *testing.T, dir string, n int) {
	t.Helper()

	for deadline := time.Now().Add(5 * time.Second); ; {
		st, _ := Call(t.Context(), dir, Request{Op: OpStatus}, 0)
		if len(st.Runs) == 1 && st.Runs[0].Drive == n {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("the drive never named itself %d: %+v", n, st)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// Past the drain deadline every drive still running is stopped, not only
// the first one the deadline reaches.
func TestDrainDeadlineStopsEveryDrive(t *testing.T) {
	dir := runDir(t)

	s := &Server{Dir: dir, Version: "v1", Child: shell, DrainWait: 200 * time.Millisecond}
	done := serve(t, s)

	for _, root := range []string{"/a", "/b"} {
		req := Request{Op: OpRun, Argv: []string{"trap '' USR1; while :; do sleep 0.05; done"}, Dir: "/", Root: root}
		if _, err := Call(t.Context(), dir, req, 0); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Call(t.Context(), dir, Request{Op: OpStop, Drain: true}, 0); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a second drive that ignores the drain kept the daemon up")
	}
}
