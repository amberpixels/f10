//go:build unix

package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// An f10 from before the Connect API says hello in JSON: the new daemon
// answers it, so the older client keeps this newer daemon instead of waiting
// out a drain, and refuses what it cannot serve with the reason.
func TestOldClientIsToldToUpdate(t *testing.T) {
	t.Parallel()

	s := &Server{Dir: runDir(t), Version: "0.62.0", Child: shell}
	serve(t, s)

	rep, err := legacyCall(t.Context(), s.Dir, legacyRequest{Op: legacyHelloOp})
	if err != nil || rep.Version != "0.62.0" || rep.PID != os.Getpid() {
		t.Fatalf("legacy hello = %+v, %v; want this daemon's version and pid", rep, err)
	}

	for _, op := range []string{"run", "status", "watch"} {
		_, err := legacyCall(t.Context(), s.Dir, legacyRequest{Op: op})
		if err == nil || !strings.Contains(err.Error(), "update f10") {
			t.Errorf("legacy %s = %v, want the update message", op, err)
		}
	}

	// the Connect clients on the same socket are unaffected
	if info, err := Hello(t.Context(), s.Dir, "0.62.0"); err != nil || info.Legacy || info.API != APIVersion {
		t.Errorf("Connect hello after legacy calls = %+v, %v", info, err)
	}
}

// oldDaemon is a daemon of the release before the Connect API: it holds the
// lease, answers the JSON protocol, and on an upgrade's drained stop leaves
// its drive for the next daemon in drained.json, the file the new daemon
// resumes from.
func oldDaemon(t *testing.T, dir string, left []drained) {
	t.Helper()

	lease, err := lock(dir, lockDaemon)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	var once sync.Once

	quit := func() {
		once.Do(func() {
			_ = ln.Close()
			_ = os.Remove(SocketPath(dir))
			lease.Release()
		})
	}
	t.Cleanup(quit)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			var req legacyRequest

			// a Connect request is not JSON: the old daemon drops it, as it did
			if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
				_ = conn.Close()

				continue
			}

			rep := legacyReply{Version: "0.61.0", PID: 1, Started: time.Now()}
			stop := req.Op == legacyStopOp

			if stop && req.Upgrade {
				raw, _ := json.Marshal(left)
				_ = os.WriteFile(drainedPath(dir), raw, 0o600)
			}

			_ = json.NewEncoder(conn).Encode(rep)
			_ = conn.Close()

			if stop {
				quit()

				return
			}
		}
	}()
}

// The acceptance path of an upgrade from the previous release: a new client
// finds the old daemon, drains it through the old protocol, starts its own,
// and the new daemon resumes the drive the old one left.
func TestNewClientUpgradesAnOldDaemon(t *testing.T) {
	t.Parallel()

	dir := runDir(t)
	oldDaemon(t, dir, []drained{{Root: "/r", Dir: "/", Drive: 5, Env: []string{"A=1"}}})

	var (
		mu  sync.Mutex
		got [][]string
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var drainedFrom string

	e := &Ensurer{
		Dir:       dir,
		Version:   "0.62.0",
		DrainWait: 5 * time.Second,
		Draining:  func(old string) { drainedFrom = old },
		Spawn: func(context.Context) error {
			next := &Server{Dir: dir, Version: "0.62.0", Child: func(argv []string) (string, []string) {
				mu.Lock()
				got = append(got, argv)
				mu.Unlock()

				return "/bin/sh", []string{"-c", "exit 0"}
			}}
			go func() { _ = next.Serve(ctx) }()

			return nil
		},
	}

	info, err := e.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if info.Version != "0.62.0" || info.Legacy || drainedFrom != "0.61.0" {
		t.Errorf("ensured %+v after draining %q, want 0.62.0 after 0.61.0", info, drainedFrom)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(got) != 1 || !slices.Equal(got[0], []string{"--resume", "5"}) {
		t.Errorf("the new daemon ran %v, want the old one's drive resumed as --resume 5", got)
	}
}

func TestAPIErrorNamesBothVersions(t *testing.T) {
	t.Parallel()

	if err := APIError(Info{Version: "0.62.0", API: APIVersion}, "0.62.0"); err != nil {
		t.Errorf("same API: %v", err)
	}

	newer := APIError(Info{Version: "0.70.0", API: APIVersion + 1}, "0.62.0")
	if newer == nil || !strings.Contains(newer.Error(), "update f10") || !strings.Contains(newer.Error(), "0.70.0") {
		t.Errorf("a daemon on a newer API: %v", newer)
	}

	older := APIError(Info{Version: "0.62.0", API: APIVersion - 1}, "0.70.0")
	if older == nil || !strings.Contains(older.Error(), "f10 daemon stop") {
		t.Errorf("a daemon on an older API: %v", older)
	}
}

// A connection sorted while the listener closes is dropped, never sent on a
// closed channel, which would panic the daemon as it stops.
func TestSniffListenerClosesWithAConnectionBeingSorted(t *testing.T) {
	t.Parallel()

	dir := runDir(t)

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	sl := newSniffListener(ln, func(c net.Conn, _ *bufio.Reader) { _ = c.Close() })

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("P"))
	time.Sleep(50 * time.Millisecond) // the sort peeks and waits to hand the connection over

	_ = ln.Close()
	<-sl.done // the accept loop has seen the close; before that, Accept may still hand a connection over

	if _, err := sl.Accept(); err == nil {
		t.Fatal("Accept after the listener closed returned a connection")
	}

	time.Sleep(50 * time.Millisecond) // a sort that sent on a closed channel would have panicked by now
}

// A call in flight, such as an event stream, keeps an otherwise idle
// daemon serving.
func TestACallInFlightKeepsTheDaemonUp(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	s := &Server{
		Dir: runDir(t), Version: "v1", Child: shell, Idle: 50 * time.Millisecond, Tick: 10 * time.Millisecond,
		Handler: func(*Server) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				<-release
				w.WriteHeader(http.StatusOK)
			})
		},
	}
	done := serve(t, s)

	go func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, SocketURL+"/stream", http.NoBody)
		if res, err := SocketHTTP(s.Dir).Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()

	select {
	case err := <-done:
		t.Fatalf("the daemon idled out with a call in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon did not idle out once the call ended")
	}
}
