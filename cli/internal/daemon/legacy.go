package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// The JSON protocol daemons spoke before the Connect API: one request, one
// reply per connection. A new client speaks it only to replace an older
// daemon, and a new daemon only to tell an older client to update. Remove
// both once no supported release speaks it.

type legacyRequest struct {
	Op      string `json:"op"`
	Drain   bool   `json:"drain,omitempty"`
	Upgrade bool   `json:"upgrade,omitempty"`
}

type legacyReply struct {
	Error    string    `json:"error,omitempty"`
	Stopping bool      `json:"stopping,omitempty"`
	Version  string    `json:"version,omitempty"`
	PID      int       `json:"pid,omitempty"`
	Started  time.Time `json:"started,omitzero"`
}

const (
	legacyHelloOp = "hello"
	legacyStopOp  = "stop"
)

func legacyCall(ctx context.Context, dir string, req legacyRequest) (legacyReply, error) {
	d := net.Dialer{Timeout: time.Second}

	conn, err := d.DialContext(ctx, "unix", SocketPath(dir))
	if err != nil {
		return legacyReply{}, fmt.Errorf("%w: %w", ErrNotRunning, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(callTimeout))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return legacyReply{}, err
	}

	var rep legacyReply
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&rep); err != nil {
		return legacyReply{}, err
	}

	if rep.Error != "" {
		return rep, errors.New(rep.Error)
	}

	return rep, nil
}

// legacyHello is an older daemon's hello, marked Legacy.
func legacyHello(ctx context.Context, dir string) (Info, error) {
	rep, err := legacyCall(ctx, dir, legacyRequest{Op: legacyHelloOp})
	if err != nil {
		return Info{}, err
	}

	return Info{Version: rep.Version, PID: rep.PID, Started: rep.Started, Stopping: rep.Stopping, Legacy: true}, nil
}

// legacyStop stops an older daemon as its own clients did.
func legacyStop(ctx context.Context, dir string, drain, upgrade bool) error {
	_, err := legacyCall(ctx, dir, legacyRequest{Op: legacyStopOp, Drain: drain, Upgrade: upgrade})

	return err
}

func legacyStatusError(version string) string {
	return fmt.Sprintf("the f10 daemon (%s) predates the Connect API: f10 daemon stop, then run the command again",
		version)
}

// A sniffListener hands the HTTP server every connection but the old
// protocol's, whose first byte opens a JSON object; those go to legacy.
type sniffListener struct {
	net.Listener

	legacy func(net.Conn, *bufio.Reader)
	conns  chan net.Conn
	errs   chan error
}

func newSniffListener(ln net.Listener, legacy func(net.Conn, *bufio.Reader)) *sniffListener {
	l := &sniffListener{Listener: ln, legacy: legacy, conns: make(chan net.Conn), errs: make(chan error, 1)}
	go l.run()

	return l
}

func (l *sniffListener) Accept() (net.Conn, error) {
	if c, ok := <-l.conns; ok {
		return c, nil
	}

	return nil, <-l.errs
}

func (l *sniffListener) run() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			l.errs <- err
			close(l.conns)

			return
		}

		go l.sort(conn)
	}
}

func (l *sniffListener) sort(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(callTimeout))

	br := bufio.NewReader(conn)

	first, err := br.Peek(1)
	if err != nil {
		_ = conn.Close()

		return
	}

	_ = conn.SetReadDeadline(time.Time{})

	if first[0] == '{' {
		l.legacy(conn, br)

		return
	}

	l.conns <- &peekedConn{Conn: conn, r: br}
}

// peekedConn reads the bytes the sniff peeked before the rest.
type peekedConn struct {
	net.Conn

	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
