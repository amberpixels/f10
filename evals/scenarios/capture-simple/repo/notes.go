package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	pageSize = 10
	maxNote  = 1 << 20
)

// list writes one page of notes, each as its name and its first line.
func (s *server) list(w http.ResponseWriter, r *http.Request) {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".txt") {
			names = append(names, e.Name())
		}
	}

	slices.Sort(names)

	start := page * pageSize
	end := min(start+pageSize, len(names))

	for _, name := range names[start:end] {
		f, err := os.Open(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}

		first, _ := bufio.NewReader(f).ReadString('\n')
		fmt.Fprintf(w, "%s\t%s\n", strings.TrimSuffix(name, ".txt"), strings.TrimSpace(first))
	}
}

// read writes one note's text.
func (s *server) read(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")

	body, err := os.ReadFile(filepath.Join(s.dir, name+".txt"))
	if err != nil {
		http.NotFound(w, r)

		return
	}

	_, _ = w.Write(body)
}

// save stores the request body as a note, refusing one over maxNote.
func (s *server) save(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		http.Error(w, "bad note name", http.StatusBadRequest)

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if len(body) > maxNote {
		http.Error(w, "note too large", http.StatusRequestEntityTooLarge)

		return
	}

	if err := os.WriteFile(filepath.Join(s.dir, name+".txt"), body, 0o600); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusCreated)
}
