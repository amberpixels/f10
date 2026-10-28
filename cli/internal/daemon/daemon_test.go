//go:build unix

package daemon

import (
	"context"
	"os"
	"slices"
	"strings"
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

// serve runs s until the test ends, and waits for it to listen.
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

func waitExit(t *testing.T, log string) int {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if code, ok := ReadExit(log); ok {
			return code
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
		Argv: []string{"echo drive 3; sleep 0.3; echo done; exit 4"},
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

	if code := waitExit(t, first.Log); code != 4 {
		t.Errorf("exit = %d, want 4", code)
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

	script := "trap 'exit 6' USR1; echo drive 5; while :; do sleep 0.05; done"

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

	if _, err := Call(t.Context(), dir, Request{Op: OpStop, Drain: true}, 0); err != nil {
		t.Fatal(err)
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if code := waitExit(t, rep.Log); code != ExitDrained {
		t.Fatalf("drained drive exited %d", code)
	}

	var (
		mu  sync.Mutex
		got [][]string
	)

	next := &Server{Dir: dir, Version: "v2", Child: func(argv []string) (string, []string) {
		mu.Lock()
		got = append(got, argv)
		mu.Unlock()

		return "/bin/sh", []string{"-c", "echo drive 5; echo \"$A\""}
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
			_, err := ensurer("v1").Ensure(ctx)
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

	e := ensurer("v2")
	e.Draining = func(old string) { drained = old }

	rep, err := e.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if rep.Version != "v2" || drained != "v1" || spawns.Load() != 2 {
		t.Errorf("replaced to %+v, drained %q, spawns %d", rep, drained, spawns.Load())
	}
}

func TestNumberWriterFindsTheDriveLine(t *testing.T) {
	found := make(chan int, 1)
	w := &numberWriter{w: &strings.Builder{}, found: func(n int) { found <- n }}

	for _, chunk := range []string{"note: model fell back\ndri", "ve 12\nGH-1 sent\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case n := <-found:
		if n != 12 {
			t.Errorf("found %d", n)
		}
	case <-time.After(time.Second):
		t.Fatal("no drive line found")
	}
}
