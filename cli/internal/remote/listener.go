package remote

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// A Listener is what the daemon asks of the tailnet listener: where to bind,
// which peers to accept, and the check in front of the API.
type Listener struct {
	Dir    string                  // the setting's and the tokens' dir
	Ifaces func() ([]Iface, error) // the machine's interfaces; Interfaces by default
	Peer   func(netip.Addr) bool   // the peers accepted; InTailnet by default
	App    http.Handler            // the web client's files, nil for none
	Certs  *Certs                  // the HTTPS certificate, nil for plain HTTP
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

// Handler puts the token check in front of h, and serves the web client's
// files beside it without one: they hold no data, and the page that asks
// for the token must load before it has one.
func (l *Listener) Handler(h http.Handler) http.Handler {
	if l.store == nil {
		l.store = NewStore(l.Dir)
	}

	return Route(Auth(l.store, h), l.App)
}

// TLS is the HTTPS config from Certs; with none, the listener serves plain
// HTTP and why says so.
func (l *Listener) TLS() (*tls.Config, string, string) {
	if l.Certs == nil {
		return nil, "", "no certificate source"
	}

	return l.Certs.Config()
}

// apiPrefixes are the Connect packages' path prefixes, which always go
// through the token check.
var apiPrefixes = []string{"/f10.v1.", "/agents.v1."}

// Route sends every Connect procedure to api, a GET or HEAD of anything
// else to app, and refuses the rest.
func Route(api, app http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range apiPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				api.ServeHTTP(w, r)

				return
			}
		}

		switch {
		case app == nil:
			http.NotFound(w, r)
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		default:
			app.ServeHTTP(w, r)
		}
	})
}
