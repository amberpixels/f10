package images

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLinks(t *testing.T) {
	cases := []struct {
		name, doc string
		want      []string
	}{
		{"none", "plain text with [a link](https://example.com/page)", nil},
		{
			"markdown", `see ![shot](https://x.io/a "title") and ![](<https://x.io/b>)`,
			[]string{"https://x.io/a", "https://x.io/b"},
		},
		{
			"html",
			`<img width="300" alt="s" src="https://github.com/user-attachments/assets/u1" /> <IMG src='https://x.io/c'>`,
			[]string{"https://github.com/user-attachments/assets/u1", "https://x.io/c"},
		},
		{
			"bare", "shot: https://s3.aws.com/f/image.PNG?X-Amz-Sig=1. and https://x.io/doc.pdf",
			[]string{"https://s3.aws.com/f/image.PNG?X-Amz-Sig=1"},
		},
		{
			"order and duplicates", "https://x.io/2.jpg\n![a](https://x.io/1.png)\n![again](https://x.io/2.jpg)",
			[]string{"https://x.io/2.jpg", "https://x.io/1.png"},
		},
		{"data and relative skipped", "![a](data:image/png;base64,AAA) ![b](./local.png)", nil},
	}

	for _, c := range cases {
		if got := Links(c.doc); !slices.Equal(got, c.want) {
			t.Errorf("%s: Links = %q, want %q", c.name, got, c.want)
		}
	}
}

// png is the smallest body http.DetectContentType reads as image/png.
var png = []byte("\x89PNG\r\n\x1a\n0000")

func TestDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/typed":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg bytes"))
		case "/sniffed.png":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(png)
		case "/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html>sign in</html>"))
		case "/auth":
			if r.Header.Get("Authorization") != "Bearer t" {
				w.WriteHeader(http.StatusForbidden)

				return
			}

			_, _ = w.Write(png)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "FT-1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(dir, "9.png")
	if err := os.WriteFile(stale, png, 0o600); err != nil {
		t.Fatal(err)
	}

	d := Downloader{Client: srv.Client(), Authorize: func(r *http.Request) {
		if r.URL.Path == "/auth" {
			r.Header.Set("Authorization", "Bearer t")
		}
	}}

	urls := []string{
		srv.URL + "/typed",
		srv.URL + "/sniffed.png",
		srv.URL + "/login",
		srv.URL + "/gone",
		srv.URL + "/auth",
	}

	got, err := d.Download(context.Background(), dir, urls)
	if err != nil {
		t.Fatal(err)
	}

	wantPaths := []string{filepath.Join(dir, "1.jpg"), filepath.Join(dir, "2.png"), "", "", filepath.Join(dir, "5.png")}
	wantErrs := []string{"", "", "not an image: text/html", "HTTP 404", ""}

	for i, r := range got {
		if r.URL != urls[i] || r.Path != wantPaths[i] || errText(r.Err) != wantErrs[i] {
			t.Errorf("result %d = %+v, want path %q, error %q", i, r, wantPaths[i], wantErrs[i])
		}
	}

	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale %s survived: %v", stale, err)
	}
}

func TestDownloadRefusesAnOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(make([]byte, maxBytes+1))
	}))
	defer srv.Close()

	got, err := Downloader{Client: srv.Client()}.Download(context.Background(), t.TempDir(), []string{srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	if errText(got[0].Err) != "over 20 MB" {
		t.Errorf("result = %+v, want the size refusal", got[0])
	}
}

func TestFooter(t *testing.T) {
	if got := Footer(nil); got != "" {
		t.Errorf("Footer(nil) = %q, want none", got)
	}

	ok := Result{URL: "https://x.io/a", Path: "/i/1.png"}
	bad := Result{URL: "https://x.io/b", Err: errors.New("HTTP 403")}

	if got, want := Footer([]Result{ok}), "Images: /i/1.png - read each before summarising\n"; got != want {
		t.Errorf("Footer(ok) = %q, want %q", got, want)
	}

	if got, want := Footer([]Result{bad}), "Images not downloaded: https://x.io/b (HTTP 403)\n"; got != want {
		t.Errorf("Footer(bad) = %q, want %q", got, want)
	}

	if got := Footer([]Result{ok, bad, ok}); strings.Count(got, "\n") != 2 ||
		!strings.Contains(got, "/i/1.png, /i/1.png") {
		t.Errorf("Footer(mixed) = %q, want both lines", got)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
