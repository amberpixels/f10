package remote

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/amberpixels/f10/cli/internal/daemon"
)

// Auth refuses every remote call that does not carry a valid token with the
// scope its procedure needs, before any handler runs. It writes Connect
// errors, so a Connect client reads the refusal as one.
func Auth(store *Store, next http.Handler) http.Handler {
	ew := connect.NewErrorWriter()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := allowed(store, r)
		if err != nil {
			_ = ew.Write(w, r, err)

			return
		}

		scopes := make([]string, len(tok.Scopes))
		for i, s := range tok.Scopes {
			scopes[i] = string(s)
		}

		next.ServeHTTP(w, r.WithContext(daemon.WithCaller(r.Context(), daemon.Caller{
			Token:      tok.Name,
			Scopes:     scopes,
			Procedures: Procedures(tok),
		})))
	})
}

// authenticate is the valid token r carries.
func authenticate(store *Store, r *http.Request) (Token, error) {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(secret) == "" {
		return Token{}, connect.NewError(connect.CodeUnauthenticated,
			errors.New("a remote call needs a device token: f10 remote token add <name> --scope <scopes>"))
	}

	tok, ok := store.Verify(strings.TrimSpace(secret))
	if !ok {
		return Token{}, connect.NewError(
			connect.CodeUnauthenticated,
			errors.New("this device token is unknown or revoked"),
		)
	}

	return tok, nil
}

func allowed(store *Store, r *http.Request) (Token, error) {
	tok, err := authenticate(store, r)
	if err != nil {
		return Token{}, err
	}

	proc := r.URL.Path
	if socketOnly[proc] {
		return Token{}, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("%s is served only on the daemon's unix socket", proc))
	}

	scope, ok := procedures[proc]
	if !ok {
		return Token{}, connect.NewError(
			connect.CodePermissionDenied,
			fmt.Errorf("%s is not served on the tailnet", proc),
		)
	}

	if scope != anyToken && !tok.Has(scope) {
		return Token{}, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("this token lacks the %s scope", scope))
	}

	return tok, nil
}
