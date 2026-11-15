package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// A file is served by its path, an app route gets index.html, and a missing
// asset is a 404 rather than the shell, so a stale page fails visibly.
func TestServeFallsBackToTheShell(t *testing.T) {
	t.Parallel()

	h := Serve(fstest.MapFS{
		"index.html":           {Data: []byte("shell")},
		"assets/app-1.js":      {Data: []byte("js")},
		"sw.js":                {Data: []byte("worker")},
		"manifest.webmanifest": {Data: []byte("{}")},
	})

	for _, c := range []struct {
		path, body, cache string
		code              int
	}{
		{"/", "shell", "no-cache", http.StatusOK},
		{"/asks", "shell", "no-cache", http.StatusOK},
		{"/task/FT-1", "shell", "no-cache", http.StatusOK},
		{"/assets/app-1.js", "js", "immutable", http.StatusOK},
		{"/sw.js", "worker", "no-cache", http.StatusOK},
		{"/assets/gone.js", "", "", http.StatusNotFound},
		{"/../../etc/passwd", "shell", "no-cache", http.StatusOK}, // cleaned to an app route, never outside dist
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, c.path, nil))

		body, _ := io.ReadAll(rec.Body)
		if rec.Code != c.code || (c.body != "" && string(body) != c.body) ||
			!strings.Contains(rec.Header().Get("Cache-Control"), c.cache) {
			t.Errorf("GET %s = %d %q cache %q, want %d %q %q",
				c.path, rec.Code, body, rec.Header().Get("Cache-Control"), c.code, c.body, c.cache)
		}
	}
}

func TestEmbeddedAppHasAShell(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("embedded / = %d, csp %q", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}
}
