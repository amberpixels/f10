package remote

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/gen/f10/v1/f10v1connect"
	"github.com/amberpixels/f10/cli/internal/daemon"
)

// The web client's files load with no token, while every Connect path
// still meets the check, and nothing but a read reaches the files.
func TestRouteKeepsTokensOnTheAPI(t *testing.T) {
	t.Parallel()

	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "app") })
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "checked", http.StatusUnauthorized)
	})

	srv := httptest.NewServer(Route(api, app))
	t.Cleanup(srv.Close)

	for _, c := range []struct {
		method, path string
		code         int
		body         string
	}{
		{http.MethodGet, "/", http.StatusOK, "app"},
		{http.MethodGet, "/asks", http.StatusOK, "app"},
		{http.MethodHead, "/assets/app.js", http.StatusOK, ""},
		{http.MethodPost, "/f10.v1.TaskService/GetTask", http.StatusUnauthorized, "checked"},
		{http.MethodGet, "/agents.v1.AskService/ListAsks", http.StatusUnauthorized, "checked"},
		{http.MethodPost, "/", http.StatusMethodNotAllowed, ""},
	} {
		req, _ := http.NewRequestWithContext(t.Context(), c.method, srv.URL+c.path, http.NoBody)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()

		if res.StatusCode != c.code || !strings.Contains(string(body), c.body) {
			t.Errorf("%s %s = %d %q, want %d %q", c.method, c.path, res.StatusCode, body, c.code, c.body)
		}
	}
}

// Procedures lists what a token's scopes reach and never a socket-only one.
func TestProceduresFollowTheScopes(t *testing.T) {
	t.Parallel()

	read := Procedures(Token{Scopes: []Scope{ScopeRead}})
	if !slices.Contains(read, f10v1connect.ArtifactServiceGetArtifactProcedure) ||
		!slices.Contains(read, f10v1connect.DaemonServiceHelloProcedure) ||
		slices.Contains(read, f10v1connect.RunServiceForwardProcedure) {
		t.Errorf("read procedures = %v", read)
	}

	all := Procedures(Token{Scopes: Scopes})
	if slices.Contains(all, f10v1connect.DaemonServiceStopDaemonProcedure) {
		t.Error("a token reaches StopDaemon")
	}

	if !slices.IsSorted(all) {
		t.Error("procedures are not sorted")
	}
}

// The check hands the token on, so Hello can say what the caller may call.
func TestAuthPutsTheCallerInTheContext(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())

	secret, err := store.Add("phone", []Scope{ScopeRead, ScopeAnswer}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var got daemon.Caller

	h := Auth(store, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = daemon.CallerOf(r.Context())
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, f10v1connect.DaemonServiceHelloProcedure, nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got.Token != "phone" || strings.Join(got.Scopes, ",") != "read,answer" ||
		!slices.Contains(got.Procedures, "/agents.v1.AskService/AnswerAsk") {
		t.Errorf("caller = %+v", got)
	}
}
