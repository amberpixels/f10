//go:build unix

package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
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
