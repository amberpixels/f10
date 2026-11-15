package remote

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
)

// A Listener is what the daemon asks of the tailnet listener: where to bind,
// which peers to accept, and the check in front of the API.
type Listener struct {
	Dir    string                  // the setting's and the tokens' dir
	Ifaces func() ([]Iface, error) // the machine's interfaces; Interfaces by default
	Peer   func(netip.Addr) bool   // the peers accepted; InTailnet by default
	store  *Store
}

// Addr is the address to bind and whether the setting is on; with no
// address, the third value says why none is wanted or none can be bound.
func (l *Listener) Addr() (string, bool, string) {
	c, err := ReadConfig(l.Dir)
	if err != nil {
		return "", false, err.Error()
	}

	if !c.Enabled {
		return "", false, "off: f10 remote on"
	}

	ifaces := l.Ifaces
	if ifaces == nil {
		ifaces = Interfaces
	}

	ip, ok := TailnetAddr(ifaces)
	if !ok {
		return "", true, "no Tailscale address on this machine: is Tailscale up?"
	}

	return net.JoinHostPort(ip.String(), strconv.Itoa(c.ListenPort())), true, ""
}

// Listen wraps ln so only allowed peers reach the API.
func (l *Listener) Listen(ln net.Listener) net.Listener {
	allow := l.Peer
	if allow == nil {
		allow = InTailnet
	}

	return peerFilter{Listener: ln, allow: allow}
}

// Handler puts the token check in front of h.
func (l *Listener) Handler(h http.Handler) http.Handler {
	if l.store == nil {
		l.store = NewStore(l.Dir)
	}

	return Auth(l.store, h)
}
