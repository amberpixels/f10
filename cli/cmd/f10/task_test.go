package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/images"
	"github.com/amberpixels/f10/cli/internal/probe"
)

// A task's images come after it as local paths, a failed one by its url and
// reason; a task with none prints as it was.
func TestWithImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shot.png" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "images", "GH-1")
	d := images.Downloader{Client: srv.Client()}

	plain := "# GH-1: no images\n\nbody\n"
	if got := withImages(context.Background(), plain, dir, d); got != plain {
		t.Errorf("withImages(plain) = %q, want it unchanged", got)
	}

	doc := "# GH-1: shots\n\n![a](" + srv.URL + "/shot.png)\n\n---\n\n**bob**\n\n" + srv.URL + "/gone.png\n"

	got := withImages(context.Background(), doc, dir, d)
	want := strings.TrimRight(doc, "\n") + "\n\n" +
		"Images: " + filepath.Join(dir, "1.png") + " - read each before summarising\n" +
		"Images not downloaded: " + srv.URL + "/gone.png (HTTP 404)\n"
	if got != want {
		t.Errorf("withImages = %q, want %q", got, want)
	}
}

// A dir that cannot be written fails every image and still prints the task.
func TestWithImagesUnwritableDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "images")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	doc := "# GH-1\n\n![a](https://x.io/a.png)\n"

	got := withImages(context.Background(), doc, filepath.Join(blocker, "GH-1"), images.Downloader{})
	if !strings.HasPrefix(got, doc) || !strings.Contains(got, "Images not downloaded: https://x.io/a.png (") {
		t.Errorf("withImages = %q, want the doc and the image named as failed", got)
	}
}

// The gh token goes to GitHub's own hosts over https and nowhere else.
func TestGithubAuthScopesTheToken(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho tok\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin)

	tg := &target{dir: t.TempDir(), pb: &probe.Probes{CLI: probe.Signal{Value: "gh"}}}

	auth := githubAuth(context.Background(), tg)
	if auth == nil {
		t.Fatal("githubAuth = nil on a gh host")
	}

	for raw, want := range map[string]string{
		"https://github.com/user-attachments/assets/u1":           "Bearer tok",
		"https://private-user-images.githubusercontent.com/1.png": "Bearer tok",
		"http://github.com/a.png":                                 "",
		"https://notgithub.com/a.png":                             "",
		"https://s3.amazonaws.com/a.png":                          "",
	} {
		r := httptest.NewRequest(http.MethodGet, raw, http.NoBody)
		auth(r)

		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("%s: Authorization = %q, want %q", raw, got, want)
		}
	}
}
