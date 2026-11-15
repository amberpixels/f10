package remote

import (
	"net"
	"net/netip"
	"strings"
)

// tailnetRange is the block Tailscale assigns its IPv4 addresses from.
var tailnetRange = netip.MustParsePrefix("100.64.0.0/10")

// InTailnet reports an address Tailscale could have assigned.
func InTailnet(ip netip.Addr) bool { return tailnetRange.Contains(ip.Unmap()) }

// An Iface is one network interface that is up, with its addresses.
type Iface struct {
	Name  string
	Addrs []net.Addr
}

// Interfaces lists the machine's interfaces that are up.
func Interfaces() ([]Iface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	out := make([]Iface, 0, len(ifs))

	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}

		out = append(out, Iface{Name: ifc.Name, Addrs: addrs})
	}

	return out, nil
}

// tunnel reports an interface Tailscale runs on: tailscale0 on Linux, a
// utun on macOS. 100.64.0.0/10 is also carrier-grade NAT space, which a
// Wi-Fi or cellular interface can hold, so the range alone is not enough.
func tunnel(name string) bool {
	return strings.HasPrefix(name, "tailscale") || strings.HasPrefix(name, "utun")
}

// TailnetAddr is this machine's Tailscale IPv4 address, read from its
// interfaces, so no tailscale CLI is needed; false when Tailscale is down.
func TailnetAddr(ifaces func() ([]Iface, error)) (netip.Addr, bool) {
	list, err := ifaces()
	if err != nil {
		return netip.Addr{}, false
	}

	for _, ifc := range list {
		if !tunnel(ifc.Name) {
			continue
		}

		if addr, ok := tailnetIn(ifc.Addrs); ok {
			return addr, true
		}
	}

	return netip.Addr{}, false
}

func tailnetIn(addrs []net.Addr) (netip.Addr, bool) {
	for _, a := range addrs {
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
