// Command notes serves a directory of text notes over HTTP.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dir := flag.String("dir", "notes", "notes directory")
	flag.Parse()

	srv := &http.Server{Addr: *addr, Handler: (&server{dir: *dir}).routes(), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
