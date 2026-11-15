// Package webui serves the web client the tailnet listener offers: a
// progressive web app built from ts/web into dist and embedded, so the page
// always matches the daemon that serves it.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var files embed.FS

// Handler serves the embedded app.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time
	}

	return Serve(sub)
}

// csp keeps the page to its own origin. Images take blob and data URLs,
// because artifacts arrive through the API and render from memory.
const csp = "default-src 'self'; img-src 'self' blob: data:; style-src 'self' 'unsafe-inline'; " +
	"frame-src blob:; connect-src 'self'; worker-src 'self'; base-uri 'none'; form-action 'none'; " +
	"frame-ancestors 'none'"

// Serve serves fsys as a single-page app: a file by its path, and
// index.html for any extensionless path, which is one of the app's routes.
func Serve(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if fi, err := fs.Stat(fsys, name); err != nil || fi.IsDir() {
			if path.Ext(name) != "" {
				http.NotFound(w, r)

				return
			}

			name = "index.html"
		}

		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")

		// hashed assets never change; the shell, worker and manifest must
		// be read again so a new release reaches an installed app
		if strings.HasPrefix(name, "assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}

		f, err := fsys.Open(name)
		if err != nil {
			http.NotFound(w, r)

			return
		}
		defer f.Close()

		fi, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)

			return
		}

		rs, ok := f.(interface {
			Read(p []byte) (int, error)
			Seek(offset int64, whence int) (int64, error)
		})
		if !ok {
			http.Error(w, "unreadable file", http.StatusInternalServerError)

			return
		}

		http.ServeContent(w, r, name, fi.ModTime(), rs)
	})
}
