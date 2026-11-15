package remote

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amberpixels/f10/cli/internal/mcp"
)

// MCPPath is where the listener serves the API as MCP tools while the
// setting has MCP on.
const MCPPath = "/mcp"

// inProcessURL is the base MCP tool calls go to; they never leave the
// process, so the host is a placeholder.
const inProcessURL = "http://f10-daemon"

// A Listener is what the daemon asks of the tailnet listener: where to bind,
// which peers to accept, and the check in front of the API.
type Listener struct {
	Dir    string                  // the setting's and the tokens' dir
	Ifaces func() ([]Iface, error) // the machine's interfaces; Interfaces by default
	Peer   func(netip.Addr) bool   // the peers accepted; InTailnet by default
	App    http.Handler            // the web client's files, nil for none
	Certs  *Certs                  // the HTTPS certificate, nil for plain HTTP
	// Version is the daemon's release, reported to MCP clients.
	Version string
	store   *Store
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
// for the token must load before it has one. While the setting has MCP on,
// it also serves MCPPath, each request's tools built from its own token.
func (l *Listener) Handler(h http.Handler) http.Handler {
	store := l.storeOf()
	api := Auth(store, h)
	routed := Route(api, l.App)
	ew := connect.NewErrorWriter()

	tools := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		tok, _ := r.Context().Value(tokenKey{}).(Token)

		return mcp.New(mcp.Options{
			Allow: Allows(tok.Scopes),
			// each call goes through the token check again, as the caller,
			// and from the caller's address, so the API still drops a remote
			// call's environment
			HTTP:    mcp.InProcess(withAuth(api, r.Header.Get("Authorization")), r.RemoteAddr),
			BaseURL: inProcessURL,
			Version: l.Version,
		})
	}, &sdk.StreamableHTTPOptions{Stateless: true})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != MCPPath || !l.mcpOn() {
			routed.ServeHTTP(w, r)

			return
		}

		tok, err := authenticate(store, r)
		if err != nil {
			_ = ew.Write(w, r, err)

			return
		}

		tools.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, tok)))
	})
}

type tokenKey struct{}

// withAuth is h with every request carrying auth, the MCP client's own header.
func withAuth(h http.Handler, auth string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", auth)
		h.ServeHTTP(w, r)
	})
}

// TLS is the HTTPS config from Certs; with none, the listener serves plain
// HTTP and why says so.
func (l *Listener) TLS() (*tls.Config, string, string) {
	if l.Certs == nil {
		return nil, "", "no certificate source"
	}

	return l.Certs.Config()
}

func (l *Listener) storeOf() *Store {
	if l.store == nil {
		l.store = NewStore(l.Dir)
	}

	return l.store
}

// mcpOn reads the setting on each request, so f10 remote on --mcp and off
// apply without a restart.
func (l *Listener) mcpOn() bool {
	c, err := ReadConfig(l.Dir)

	return err == nil && c.MCP
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
