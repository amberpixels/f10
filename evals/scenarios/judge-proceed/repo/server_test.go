package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	(&server{dir: t.TempDir()}).routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("health = %d", rec.Code)
	}
}

func TestSaveThenRead(t *testing.T) {
	h := (&server{dir: t.TempDir()}).routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/note?name=todo", strings.NewReader("milk\n")))

	if rec.Code != http.StatusCreated {
		t.Fatalf("save = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/note?name=todo", nil))

	if rec.Body.String() != "milk\n" {
		t.Fatalf("read = %q", rec.Body.String())
	}
}
