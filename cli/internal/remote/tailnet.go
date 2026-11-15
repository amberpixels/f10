package remote

import (
	"net"
	"net/netip"
)

// tailnetRange is the block Tailscale assigns its IPv4 addresses from.
var tailnetRange = netip.MustParsePrefix("100.64.0.0/10")

// InTailnet reports an address Tailscale could have assigned.
func InTailnet(ip netip.Addr) bool { return tailnetRange.Contains(ip.Unmap()) }

// TailnetAddr is this machine's Tailscale IPv4 address, read from its
// interfaces, so no tailscale CLI is needed; false when Tailscale is down.
func TailnetAddr(addrs func() ([]net.Addr, error)) (netip.Addr, bool) {
	list, err := addrs()
	if err != nil {
		return netip.Addr{}, false
	}

	for _, a := range list {
		var ip net.IP

		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}

		if addr, ok := netip.AddrFromSlice(ip); ok && InTailnet(addr) {
			return addr.Unmap(), true
		}
	}

	return netip.Addr{}, false
}

// peerFilter closes every connection from outside the allowed peers before
// a byte is read. Binding the tailnet address alone is not enough: macOS
// accepts a packet for a local address on any interface.
type peerFilter struct {
	net.Listener

	allow func(netip.Addr) bool
}

func (l peerFilter) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		if ap, err := netip.ParseAddrPort(conn.RemoteAddr().String()); err == nil && l.allow(ap.Addr()) {
			return conn, nil
		}

		_ = conn.Close()
	}
}
