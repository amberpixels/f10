package remote

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func cidr(t *testing.T, s string) net.Addr {
	t.Helper()

	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}

	n.IP = ip

	return n
}

func TestTailnetAddrPicksTheTailscaleAddress(t *testing.T) {
	t.Parallel()

	addrs := func() ([]net.Addr, error) {
		return []net.Addr{cidr(t, "127.0.0.1/8"), cidr(t, "192.168.1.5/24"), cidr(t, "100.101.102.103/32")}, nil
	}

	if got, ok := TailnetAddr(addrs); !ok || got.String() != "100.101.102.103" {
		t.Errorf("TailnetAddr = %v, %v; want 100.101.102.103", got, ok)
	}

	lan := func() ([]net.Addr, error) { return []net.Addr{cidr(t, "192.168.1.5/24")}, nil }
	if got, ok := TailnetAddr(lan); ok {
		t.Errorf("TailnetAddr with no Tailscale = %v, want none", got)
	}

	broken := func() ([]net.Addr, error) { return nil, errors.New("no") }
	if _, ok := TailnetAddr(broken); ok {
		t.Error("TailnetAddr found an address when the interfaces did not read")
	}
}

// A peer outside the allowed range is closed before the server reads a byte.
func TestPeerFilterClosesOutsiders(t *testing.T) {
	t.Parallel()

	for _, allow := range []bool{false, true} {
		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		pf := peerFilter{Listener: ln, allow: func(netip.Addr) bool { return allow }}

		go func() {
			conn, err := pf.Accept()
			if err != nil {
				return
			}

			_, _ = conn.Write([]byte("hi"))
			_ = conn.Close()
		}()

		conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		got, _ := io.ReadAll(conn)
		_ = conn.Close()
		_ = ln.Close()

		if allow != (string(got) == "hi") {
			t.Errorf("allow=%v: the peer read %q", allow, got)
		}
	}
}

func TestListenerAddrFollowsTheSetting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ts := func() ([]net.Addr, error) { return []net.Addr{cidr(t, "100.64.0.7/32")}, nil }
	l := &Listener{Dir: dir, Addrs: ts}

	if addr, on, _ := l.Addr(); addr != "" || on {
		t.Errorf("no setting: Addr = %q, %v; want off", addr, on)
	}

	if err := WriteConfig(dir, Config{Enabled: true, Port: 9000}); err != nil {
		t.Fatal(err)
	}

	if addr, on, _ := l.Addr(); addr != "100.64.0.7:9000" || !on {
		t.Errorf("on: Addr = %q, %v; want 100.64.0.7:9000", addr, on)
	}

	l.Addrs = func() ([]net.Addr, error) { return nil, nil }
	if addr, on, why := l.Addr(); addr != "" || !on || why == "" {
		t.Errorf("on with Tailscale down: Addr = %q, %v, %q; want none, with a reason", addr, on, why)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if addr, _, why := l.Addr(); addr != "" || why == "" {
		t.Errorf("broken setting: Addr = %q, %q; want none, with the error", addr, why)
	}
}
