//go:build unix

package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/daemon"
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

func serving(ctx context.Context, dir, version string) func(context.Context) error {
	return func(context.Context) error {
		go func() { _ = (&daemon.Server{Dir: dir, Version: version, Child: scriptChild}).Serve(ctx) }()

		return nil
	}
}

func awaitDrive(t *testing.T, dir string, n int) {
	t.Helper()

	for deadline := time.Now().Add(5 * time.Second); ; {
		st, _ := daemon.Call(t.Context(), dir, daemon.Request{Op: daemon.OpStatus}, 0)
		if len(st.Runs) == 1 && st.Runs[0].Drive == n {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("drive %d never ran: %+v", n, st)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func runReq(script string) daemon.Request {
	return daemon.Request{Op: daemon.OpRun, Argv: []string{script}, Dir: "/", Root: "/r", Key: script}
}

func TestFollowDriveEndsWithItsCode(t *testing.T) {
	dir := shortRunDir(t)

	var out syncBuffer

	err := followDrive(
		t.Context(),
		&out,
		dir,
		"0.1.0",
		runReq("echo drive 3; echo asks; exit 4"),
		serving(t.Context(), dir, "0.1.0"),
	)
	if exitCode(err) != exitAsk {
		t.Fatalf("err = %v, want exit %d", err, exitAsk)
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

	go func() { done <- followDrive(t.Context(), &out, dir, "0.1.0", runReq(waitsForDrain), counted) }()

	awaitDrive(t, dir, 5)

	if _, err := daemon.Call(t.Context(), dir, daemon.Request{Op: daemon.OpStop, Drain: true}, 0); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if exitCode(err) != exitDrained {
			t.Fatalf("err = %v, want exit %d", err, exitDrained)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the follower did not end on a stop")
	}

	if !strings.Contains(out.String(), "f10 drive --resume 5 goes on") {
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

	go func() { done <- followDrive(t.Context(), &out, dir, "0.1.0", runReq(waitsForDrain), old) }()

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

	st, err := daemon.Call(t.Context(), dir, daemon.Request{Op: daemon.OpHello}, 0)
	if err != nil || st.Version != "0.2.0" {
		t.Errorf("daemon left running = %+v, %v", st, err)
	}
}
