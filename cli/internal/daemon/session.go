//go:build unix

package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

func sessionPath(dir string) string { return filepath.Join(dir, "session.json") }

// remember keeps env as the session the daemon's own Herdr calls run
// under, when it comes from inside one; the caller holds mu. The daemon
// outlives the client that spawned it, so the newest session is the one
// most likely still alive.
func (s *Server) remember(env []string) {
	if !slices.Contains(env, "HERDR_ENV=1") || slices.Equal(env, s.session) {
		return
	}

	s.session = slices.Clone(env)

	raw, err := json.Marshal(s.session)
	if err == nil {
		err = os.WriteFile(sessionPath(s.Dir), raw, 0o600)
	}

	if err != nil {
		s.Log.Printf("keeping the session environment: %v", err)
	}
}

// loadSession reads the session a previous daemon kept.
func (s *Server) loadSession() {
	raw, err := os.ReadFile(sessionPath(s.Dir))
	if err != nil {
		return
	}

	var env []string
	if json.Unmarshal(raw, &env) == nil {
		s.mu.Lock()
		s.session = env
		s.mu.Unlock()
	}
}

// SessionEnv is the newest local session's environment, nil before any
// client registered one.
func (s *Server) SessionEnv() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.session)
}

// legacy answers a client of a release before the Connect API: hello, so it
// keeps this newer daemon by the release rule instead of waiting out a
// drain, stop as before, and every other op with the reason it cannot work.
func (s *Server) legacy(conn net.Conn, br *bufio.Reader) {
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(callTimeout))

	var req legacyRequest
	if err := json.NewDecoder(br).Decode(&req); err != nil {
		return
	}

	info := s.Info()
	rep := legacyReply{Version: info.Version, PID: info.PID, Started: info.Started}

	switch req.Op {
	case legacyHelloOp:
	case legacyStopOp:
		s.Stop(req.Drain, req.Upgrade)
	default:
		rep.Error = "this f10 is older than the running daemon (" + info.Version +
			"), which serves the Connect API: update f10"
	}

	rep.Stopping = s.Info().Stopping

	_ = conn.SetWriteDeadline(time.Now().Add(callTimeout))
	_ = json.NewEncoder(conn).Encode(rep)
}
