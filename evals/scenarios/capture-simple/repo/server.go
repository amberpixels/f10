package main

import "net/http"

type server struct {
	dir string
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /notes", s.list)
	mux.HandleFunc("GET /note", s.read)
	mux.HandleFunc("POST /note", s.save)

	return mux
}
