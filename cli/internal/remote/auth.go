package remote

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
)

// Auth refuses every remote call that does not carry a valid token with the
// scope its procedure needs, before any handler runs. It writes Connect
// errors, so a Connect client reads the refusal as one.
func Auth(store *Store, next http.Handler) http.Handler {
	ew := connect.NewErrorWriter()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := allowed(store, r); err != nil {
			_ = ew.Write(w, r, err)

			return
		}

		next.ServeHTTP(w, r)
	})
}

func allowed(store *Store, r *http.Request) error {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(secret) == "" {
		return connect.NewError(connect.CodeUnauthenticated,
			errors.New("a remote call needs a device token: f10 remote token add <name> --scope <scopes>"))
	}

	tok, ok := store.Verify(strings.TrimSpace(secret))
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("this device token is unknown or revoked"))
	}

	proc := r.URL.Path
	if socketOnly[proc] {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("%s is served only on the daemon's unix socket", proc))
	}

	scope, ok := procedures[proc]
	if !ok {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("%s is not served on the tailnet", proc))
	}

	if scope != anyToken && !tok.Has(scope) {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("this token lacks the %s scope", scope))
	}

	return nil
}
