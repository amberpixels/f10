//go:build unix

package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/flock"
)

// remoteServer is the tailnet listener while one is bound.
type remoteServer struct {
	addr string
	tls  bool
	srv  *http.Server
}

// reconcileRemote binds, rebinds or closes the tailnet listener to match
// what Remote wants now: the setting turned on or off, Tailscale coming up
// after login, or its address changing.
func (s *Server) reconcileRemote() {
	if s.Remote == nil {
		return
	}

	// held across reading the setting and applying it: a tick that read
	// "on" must not bind after a status call applied a newer "off"
	s.rmu.Lock()
	defer s.rmu.Unlock()

	addr, on, why := s.Remote.Addr()

	// outside mu: a certificate may be fetched first
	var (
		cfg          *tls.Config
		host, tlsWhy string
	)

	if t, ok := s.Remote.(RemoteTLS); ok && addr != "" {
		cfg, host, tlsWhy = t.TLS()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.remote != nil && s.remote.addr == addr && s.remote.tls == (cfg != nil) {
		s.remoteAt = boundState(on, addr, cfg, host, tlsWhy)

		return
	}

	if s.remote != nil {
		_ = s.remote.srv.Close()
		s.Log.Printf("tailnet listener on %s closed", s.remote.addr)
		s.remote = nil
	}

	if addr != "" {
		r, err := s.listenRemote(addr, cfg, host)
		if err == nil {
			s.remote, s.remoteAt = r, boundState(on, addr, cfg, host, tlsWhy)
			s.Log.Printf("tailnet listener on %s", s.remoteAt.URL)

			return
		}

		why = err.Error()
	}

	// logged once per reason, since this runs every tick
	if on && why != s.remoteAt.Reason {
		s.Log.Printf("tailnet listener down: %s", why)
	}

	s.remoteAt = RemoteState{Enabled: on, Reason: why}
}

// listenRemote serves the API on addr, behind Remote's peer filter and
// token check, over TLS when cfg is set; the caller holds mu.
func (s *Server) listenRemote(addr string, cfg *tls.Config, host string) (*remoteServer, error) {
	ln, err := (&net.ListenConfig{}).Listen(s.ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}

	// the peer filter sees the raw connection, before any handshake
	ln = s.Remote.Listen(ln)
	if cfg != nil {
		_, port, _ := net.SplitHostPort(addr)
		ln = tls.NewListener(redirectPlain(ln, "https://"+net.JoinHostPort(host, port)), cfg)
	}

	// an idle peer's connection is closed, so refused keep-alives cannot
	// pile up and use the daemon's files
	srv := &http.Server{
		Handler:           s.Remote.Handler(s.api),
		ReadHeaderTimeout: callTimeout,
		IdleTimeout:       time.Minute,
	}

	go func() { _ = srv.Serve(ln) }()

	return &remoteServer{addr: addr, tls: cfg != nil, srv: srv}, nil
}

// boundState is the listener bound on addr: its URL names the
// certificate's host over HTTPS, else the address itself.
func boundState(on bool, addr string, cfg *tls.Config, host, tlsWhy string) RemoteState {
	st := RemoteState{Enabled: on, Addr: addr, URL: "http://" + addr + "/", HTTPSReason: tlsWhy}

	if cfg != nil {
		_, port, _ := net.SplitHostPort(addr)
		st.URL, st.HTTPS, st.HTTPSReason = "https://"+net.JoinHostPort(host, port)+"/", true, ""
	}

	return st
}

// lease takes the daemon's lease. A plain daemon finding it held is
// ErrRunning; a service waits for it, and a nil lease with no error means
// ctx ended while it waited.
func (s *Server) lease(ctx context.Context) (*flock.Lease, error) {
	last := ""

	for {
		own, err := lock(s.Dir, lockDaemon)

		held := flock.HeldError{}
		if err != nil && !errors.As(err, &held) {
			return nil, err
		}

		why := "another f10 daemon holds the lease"

		if err == nil {
			if why = s.yields(); why == "" {
				return own, nil
			}

			own.Release()
		}

		if !s.Service {
			return nil, ErrRunning
		}

		if why != last {
			s.Log.Printf("standing by: %s", why)
			last = why
		}

		select {
		case <-ctx.Done():
			return nil, nil //nolint:nilnil // stopped while standing by: nothing to serve
		case <-time.After(s.Tick):
		}
	}
}

// yields is why a service must not serve: a newer client asked for an
// upgrade, and launchd restarting an older binary must not take the drained
// drives from the daemon that client starts. "" for none.
func (s *Server) yields() string {
	if !s.Service {
		return ""
	}

	newest, at := readNewest(s.Dir)
	if Newer(newest, s.Version) && time.Since(at) < newestHold {
		return fmt.Sprintf("f10 %s asked for an upgrade and this is %s", newest, s.Version)
	}

	return ""
}

// newestHold is how long an upgrade keeps an older service standing by:
// long enough for the upgrading client to start its own daemon, short
// enough that a service left on an old release still serves after a reboot.
const newestHold = 2 * time.Minute

func newestPath(dir string) string { return filepath.Join(dir, "newest") }

// writeNewest records the version of a client that drains an older daemon.
func writeNewest(dir, version string) error {
	return os.WriteFile(newestPath(dir), []byte(version+"\n"), 0o600)
}

// readNewest is the version an upgrade recorded and when.
func readNewest(dir string) (string, time.Time) {
	path := newestPath(dir)

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}
	}

	fi, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}
	}

	return strings.TrimSpace(string(raw)), fi.ModTime()
}
