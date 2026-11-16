//go:build unix

package daemon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
)

// fakeRemote stands in for the tailnet listener on loopback, so the tests
// need no Tailscale.
type fakeRemote struct {
	mu   sync.Mutex
	addr string
	on   bool
	why  string
}

func (f *fakeRemote) Addr() (string, bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.addr, f.on, f.why
}

func (*fakeRemote) Listen(ln net.Listener) net.Listener { return ln }

// Handler marks what passed it, so a test sees the check sits in front.
func (*fakeRemote) Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ok" {
			http.Error(w, "no", http.StatusUnauthorized)

			return
		}

		h.ServeHTTP(w, r)
	})
}

func (f *fakeRemote) set(addr string, on bool, why string) {
	f.mu.Lock()
	f.addr, f.on, f.why = addr, on, why
	f.mu.Unlock()
}

// freeAddr is a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	return addr
}

func remoteHello(ctx context.Context, addr, token string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	req := connect.NewRequest(&f10v1.HelloRequest{Version: "test", ApiVersion: APIVersion})
	req.Header().Set("Authorization", "Bearer "+token)

	_, err := f10v1connect.NewDaemonServiceClient(http.DefaultClient, "http://"+addr).Hello(ctx, req)

	return err
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// The listener is up once Serve listens, not a tick later, serves the same
// API as the socket behind the remote check, and closes when the setting
// goes off.
func TestRemoteListenerFollowsTheSetting(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	fr := &fakeRemote{addr: addr, on: true}

	s := &Server{Dir: runDir(t), Version: "v1", Child: shell, Remote: fr, Tick: time.Hour}
	serve(t, s)

	if err := remoteHello(t.Context(), addr, "ok"); err != nil {
		t.Fatalf("hello on the tailnet listener at start = %v, want an answer", err)
	}

	if err := remoteHello(t.Context(), addr, "bad"); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("hello with a bad token = %v, want the remote check's refusal", err)
	}

	st, err := GetStatus(t.Context(), s.Dir, "test")
	if err != nil || st.Remote.Addr != addr || !st.Remote.Enabled {
		t.Fatalf("status remote = %+v, %v; want %s bound", st.Remote, err, addr)
	}

	fr.set("", false, "off: f10 remote on")

	// a status call reconciles, as f10 remote off relies on
	st, err = GetStatus(t.Context(), s.Dir, "test")
	if err != nil || st.Remote.Addr != "" || st.Remote.Enabled {
		t.Fatalf("status remote after off = %+v, %v; want none bound", st.Remote, err)
	}

	if err := remoteHello(t.Context(), addr, "ok"); err == nil {
		t.Error("the tailnet listener still answers after the setting went off")
	}
}

// A changed address rebinds on the next tick, and a missing one is
// reported with its reason.
func TestRemoteListenerRebindsOnTick(t *testing.T) {
	t.Parallel()

	first, second := freeAddr(t), freeAddr(t)
	fr := &fakeRemote{on: true, why: "no Tailscale address on this machine"}

	s := &Server{Dir: runDir(t), Version: "v1", Child: shell, Remote: fr, Tick: 10 * time.Millisecond}
	serve(t, s)

	if st := s.Status(); st.Remote.Addr != "" || !strings.Contains(st.Remote.Reason, "Tailscale") {
		t.Errorf("remote with no address = %+v, want the reason", st.Remote)
	}

	fr.set(first, true, "")
	answers := func(addr string) func() bool {
		return func() bool { return remoteHello(t.Context(), addr, "ok") == nil }
	}

	eventually(t, "the listener never came up on a tick", answers(first))

	fr.set(second, true, "")
	eventually(t, "the listener never moved", answers(second))

	if err := remoteHello(t.Context(), first, "ok"); err == nil {
		t.Error("the old address still answers after the rebind")
	}
}

// With the listener's setting on, or as a service, the daemon stays up with
// nothing to do.
func TestRemoteAndServiceNeverIdle(t *testing.T) {
	t.Parallel()

	for name, s := range map[string]*Server{
		"listener on": {Remote: &fakeRemote{on: true, why: "no Tailscale address"}},
		"service":     {Service: true},
	} {
		s.Dir, s.Version, s.Child = runDir(t), "v1", shell
		s.Idle, s.Tick = 20*time.Millisecond, 5*time.Millisecond

		done := serve(t, s)

		select {
		case err := <-done:
			t.Errorf("%s: the daemon exited idle (%v)", name, err)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// A service finding the lease held stands by, and serves once the holder
// exits.
func TestServiceStandsByAndTakesOver(t *testing.T) {
	t.Parallel()

	dir := runDir(t)

	ctx, cancel := context.WithCancel(t.Context())
	first := &Server{Dir: dir, Version: "v1", Child: shell}
	register(first)

	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Serve(ctx) }()

	eventually(t, "the first daemon never listened", func() bool { return listening(t.Context(), dir) == nil })

	standby := &Server{Dir: dir, Version: "v1", Child: shell, Service: true, Tick: 10 * time.Millisecond}
	standbyDone := make(chan error, 1)

	go func() { standbyDone <- standby.Serve(t.Context()) }()

	select {
	case err := <-standbyDone:
		t.Fatalf("the service returned with the lease held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()

	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}

	eventually(t, "the standby never took over", func() bool {
		info, err := Hello(t.Context(), dir, "test")

		return err == nil && info.PID > 0 && !info.Stopping
	})
}

// A service older than the client that asked for an upgrade never serves,
// so launchd restarting an old binary cannot take the drained drives back.
func TestServiceOlderThanTheUpgradeStandsBy(t *testing.T) {
	t.Parallel()

	dir := runDir(t)
	if err := writeNewest(dir, "0.3.0"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	old := &Server{Dir: dir, Version: "0.2.0", Child: shell, Service: true, Tick: 10 * time.Millisecond}
	done := make(chan error, 1)

	go func() { done <- old.Serve(ctx) }()

	time.Sleep(200 * time.Millisecond)

	if err := listening(t.Context(), dir); err == nil {
		t.Error("a service older than the upgrade serves")
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("a standby ended by its context = %v, want nil", err)
	}

	// past the hold, an old service serves again, so one left on an old
	// release after a reboot still brings the listener back
	old2 := time.Now().Add(-2 * newestHold)
	if err := os.Chtimes(newestPath(dir), old2, old2); err != nil {
		t.Fatal(err)
	}

	stale := &Server{Dir: dir, Version: "0.2.0", Child: shell, Service: true, Tick: 10 * time.Millisecond}
	serve(t, stale)
}

// Only the socket passes its environment: a call over TCP, which is how
// the tailnet reaches the daemon, gets none, so a token cannot hand a drive
// variables such as GIT_SSH_COMMAND.
func TestLocalEnvOnlyFromTheSocket(t *testing.T) {
	t.Parallel()

	echo := &envEcho{}
	mux := http.NewServeMux()
	mux.Handle(f10v1connect.NewDaemonServiceHandler(echo))

	call := func(hc *http.Client, base string) string {
		res, err := f10v1connect.NewDaemonServiceClient(hc, base).WatchProject(t.Context(),
			connect.NewRequest(&f10v1.WatchProjectRequest{Env: []string{"GIT_SSH_COMMAND=evil"}}))
		if err != nil {
			t.Fatal(err)
		}

		return res.Msg.GetLog()
	}

	tcp := httptest.NewServer(mux)
	t.Cleanup(tcp.Close)

	if got := call(http.DefaultClient, tcp.URL); got != "" {
		t.Errorf("env over TCP = %q, want none", got)
	}

	dir := runDir(t)

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	if got := call(SocketHTTP(dir), SocketURL); got != "GIT_SSH_COMMAND=evil" {
		t.Errorf("env over the socket = %q, want it kept", got)
	}
}

// envEcho answers WatchProject with the environment LocalEnv let through.
type envEcho struct {
	f10v1connect.UnimplementedDaemonServiceHandler
}

func (*envEcho) WatchProject(
	_ context.Context,
	req *connect.Request[f10v1.WatchProjectRequest],
) (*connect.Response[f10v1.WatchProjectResponse], error) {
	return connect.NewResponse(&f10v1.WatchProjectResponse{
		Log: strings.Join(LocalEnv(req, req.Msg.GetEnv()), ","),
	}), nil
}

// tlsRemote is a fakeRemote that holds a certificate once given one.
type tlsRemote struct {
	fakeRemote

	cfg *tls.Config
}

func (f *tlsRemote) TLS() (*tls.Config, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cfg == nil {
		return nil, "", "no certificate yet"
	}

	return f.cfg, "box.tail.ts.net", ""
}

// The listener serves plain HTTP until a certificate arrives, then rebinds
// to HTTPS on the next tick and names the certificate's host in its URL.
func TestRemoteListenerSwitchesToTLS(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	fr := &tlsRemote{addr: addr, on: true}

	s := &Server{Dir: runDir(t), Version: "v1", Child: shell, Remote: fr, Tick: 10 * time.Millisecond}
	serve(t, s)

	if st := s.Status(); st.Remote.HTTPS || st.Remote.URL != "http://"+addr+"/" ||
		st.Remote.HTTPSReason != "no certificate yet" {
		t.Fatalf("remote before a certificate = %+v, want plain HTTP with the reason", st.Remote)
	}

	pool, cert := testCert(t)

	fr.mu.Lock()
	fr.cfg = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	fr.mu.Unlock()

	_, port, _ := net.SplitHostPort(addr)
	eventually(t, "the listener never switched to HTTPS", func() bool {
		return s.Status().Remote.URL == "https://box.tail.ts.net:"+port+"/"
	})

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: pool, ServerName: "box.tail.ts.net", MinVersion: tls.VersionTLS12,
	}}}

	req := connect.NewRequest(&f10v1.HelloRequest{Version: "test", ApiVersion: APIVersion})
	req.Header().Set("Authorization", "Bearer ok")

	if _, err := f10v1connect.NewDaemonServiceClient(client, "https://"+addr).Hello(t.Context(), req); err != nil {
		t.Fatalf("hello over HTTPS = %v", err)
	}

	// the http:// address printed before the certificate came still leads to the page
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	get, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/asks?x=1", http.NoBody)

	res, err := plain.Do(get)
	if err != nil {
		t.Fatal(err)
	}

	_ = res.Body.Close()

	if res.StatusCode != http.StatusPermanentRedirect ||
		res.Header.Get("Location") != "https://box.tail.ts.net:"+port+"/asks?x=1" {
		t.Errorf("plain HTTP = %d to %q, want a redirect to the https address",
			res.StatusCode, res.Header.Get("Location"))
	}
}

// testCert is a self-signed certificate for box.tail.ts.net and the pool
// that trusts it.
func testCert(t *testing.T) (*x509.CertPool, tls.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "box.tail.ts.net"},
		DNSNames:     []string{"box.tail.ts.net"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	return pool, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// Hello says who calls: the socket is local, and a caller the tailnet's
// check put in the context comes back with its token and procedures.
func TestHelloNamesTheCaller(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	s := &Server{Dir: runDir(t), Version: "v1", Child: shell, Remote: &callerRemote{fakeRemote{addr: addr, on: true}}}
	serve(t, s)

	hello, err := Dial(s.Dir).Daemon.Hello(t.Context(), connect.NewRequest(&f10v1.HelloRequest{}))
	if err != nil || !hello.Msg.GetCaller().GetLocal() {
		t.Fatalf("socket caller = %v, %v; want local", hello, err)
	}

	req := connect.NewRequest(&f10v1.HelloRequest{Version: "test", ApiVersion: APIVersion})
	req.Header().Set("Authorization", "Bearer ok")

	res, err := f10v1connect.NewDaemonServiceClient(http.DefaultClient, "http://"+addr).Hello(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}

	c := res.Msg.GetCaller()
	if c.GetLocal() || c.GetToken() != "phone" || len(c.GetProcedures()) != 1 {
		t.Errorf("tailnet caller = %v, want the phone token and its procedure", c)
	}
}

// callerRemote marks each call as the phone token's, as remote.Auth does.
type callerRemote struct{ fakeRemote }

func (f *callerRemote) Handler(h http.Handler) http.Handler {
	return f.fakeRemote.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(WithCaller(r.Context(), Caller{
			Token: "phone", Scopes: []string{"read"}, Procedures: []string{"/f10.v1.DaemonService/Hello"},
		})))
	}))
}
