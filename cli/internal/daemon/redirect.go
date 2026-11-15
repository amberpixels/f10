//go:build unix

package daemon

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// tlsRecord is the first byte of a TLS handshake record.
const tlsRecord = 0x16

// sortTimeout bounds how long a connection may wait before its first byte.
const sortTimeout = 10 * time.Second

// redirectPlain hands on only the connections that open with a TLS
// handshake, and answers a plain-HTTP request with a redirect to the same
// path under base, so the http:// address printed before the certificate
// came keeps leading to the page. Each connection is sorted on its own
// goroutine, so a slow peer never holds up the others.
func redirectPlain(ln net.Listener, base string) net.Listener {
	r := &plainRedirect{Listener: ln, base: base, conns: make(chan net.Conn), done: make(chan struct{})}
	go r.loop()

	return r
}

type plainRedirect struct {
	net.Listener

	base  string
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
	err   error
}

func (r *plainRedirect) Accept() (net.Conn, error) {
	select {
	case c := <-r.conns:
		return c, nil
	case <-r.done:
		return nil, r.err
	}
}

func (r *plainRedirect) loop() {
	for {
		c, err := r.Listener.Accept()
		if err != nil {
			r.err = err
			r.once.Do(func() { close(r.done) })

			return
		}

		go r.sort(c)
	}
}

func (r *plainRedirect) sort(c net.Conn) {
	br := bufio.NewReader(c)

	_ = c.SetReadDeadline(time.Now().Add(sortTimeout))
	first, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})

	switch {
	case err != nil:
		_ = c.Close()
	case first[0] == tlsRecord:
		select {
		case r.conns <- &peeked{Conn: c, r: br}:
		case <-r.done:
			_ = c.Close()
		}
	default:
		r.redirect(c, br)
	}
}

func (r *plainRedirect) redirect(c net.Conn, br *bufio.Reader) {
	defer c.Close()

	_ = c.SetDeadline(time.Now().Add(sortTimeout))

	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	_, _ = fmt.Fprintf(c, "HTTP/1.1 308 Permanent Redirect\r\nLocation: %s%s\r\n"+
		"Content-Length: 0\r\nConnection: close\r\n\r\n", r.base, req.URL.RequestURI())
}

// peeked is a connection whose first bytes sit in r.
type peeked struct {
	net.Conn

	r *bufio.Reader
}

func (p *peeked) Read(b []byte) (int, error) { return p.r.Read(b) }
