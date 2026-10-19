// Package images downloads the images a task links, so an agent reads
// local files instead of fetching urls that expire (Notion's signed links)
// or need auth its fetch tool lacks (a private repo's attachments).
package images

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxBytes    = 20 << 20
	timeout     = 30 * time.Second
	parallelism = 4
)

var (
	// ![alt](url "title"), the url optionally in angle brackets
	markdownImage = regexp.MustCompile(`!\[[^\]]*\]\(\s*<?(https?://[^\s)>]+)>?`)
	// <img src="url">, what GitHub writes for a pasted screenshot
	htmlImage = regexp.MustCompile(`(?i)<img\b[^>]*?\bsrc\s*=\s*["']?(https?://[^"'\s>]+)`)
	// a bare url, an image only when its path ends in an image extension
	bareURL = regexp.MustCompile(`https?://[^\s<>()"'\[\]]+`)
)

// byType names a file by its content type; a type missing here falls back
// to the url's own extension.
var byType = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/svg+xml": ".svg",
	"image/bmp":     ".bmp",
}

// Links is every image url doc links, in order of first appearance, each
// once: markdown and HTML image syntax with any url, and bare urls whose
// path ends in an image extension.
func Links(doc string) []string {
	type hit struct {
		at  int
		url string
	}

	var hits []hit

	for _, re := range []*regexp.Regexp{markdownImage, htmlImage} {
		for _, m := range re.FindAllStringSubmatchIndex(doc, -1) {
			hits = append(hits, hit{m[2], doc[m[2]:m[3]]})
		}
	}

	for _, m := range bareURL.FindAllStringIndex(doc, -1) {
		if u := strings.TrimRight(doc[m[0]:m[1]], ".,;:!?*_`"); imageExt(u) != "" {
			hits = append(hits, hit{m[0], u})
		}
	}

	slices.SortStableFunc(hits, func(a, b hit) int { return a.at - b.at })

	var out []string

	for _, h := range hits {
		if !slices.Contains(out, h.url) {
			out = append(out, h.url)
		}
	}

	return out
}

// imageExt is the url path's extension when it names an image, else "".
func imageExt(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	ext := strings.ToLower(path.Ext(u.Path))
	if ext == ".jpeg" {
		return ".jpg"
	}

	for _, known := range byType {
		if ext == known {
			return ext
		}
	}

	return ""
}

// A Result is one image: where it landed, or why it did not.
type Result struct {
	URL  string
	Path string
	Err  error
}

// A Downloader fetches images. Authorize, when set, may add credentials to
// each first request; the client decides what survives a redirect.
type Downloader struct {
	Client    *http.Client
	Authorize func(*http.Request)
}

// Download fetches every url into dir, named 1.<ext>, 2.<ext>, ... in link
// order. dir is emptied first, so it holds only this set.
func (d Downloader) Download(ctx context.Context, dir string, urls []string) ([]Result, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("clearing %s: %w", dir, err)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}

	results := make([]Result, len(urls))
	sem := make(chan struct{}, parallelism)

	var wg sync.WaitGroup

	for i, u := range urls {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			p, err := d.fetch(ctx, u, filepath.Join(dir, strconv.Itoa(i+1)))
			results[i] = Result{URL: u, Path: p, Err: err}
		})
	}

	wg.Wait()

	return results, nil
}

// fetch downloads one image to base plus the extension its type gives.
func (d Downloader) fetch(ctx context.Context, raw, base string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, http.NoBody)
	if err != nil {
		return "", err
	}

	if d.Authorize != nil {
		d.Authorize(req)
	}

	resp, err := cmp.Or(d.Client, http.DefaultClient).Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			return "", ue.Err
		}

		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", err
	}

	if len(body) > maxBytes {
		return "", fmt.Errorf("over %d MB", maxBytes>>20)
	}

	ext, err := extension(resp.Header.Get("Content-Type"), body, raw)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(base+ext, body, 0o600); err != nil {
		return "", err
	}

	return base + ext, nil
}

// extension is the file extension for an image body, from its declared
// type, else its sniffed one; a body that is no image is an error. A login
// page served instead of the image is the usual case.
func extension(declared string, body []byte, raw string) (string, error) {
	typ, _, _ := mime.ParseMediaType(declared)
	if !strings.HasPrefix(typ, "image/") {
		sniffed, _, _ := mime.ParseMediaType(http.DetectContentType(body))
		if !strings.HasPrefix(sniffed, "image/") {
			return "", fmt.Errorf("not an image: %s", cmp.Or(typ, sniffed))
		}

		typ = sniffed
	}

	return cmp.Or(byType[typ], imageExt(raw)), nil
}

// Footer is what follows the document: a line naming each local path, and
// a line naming each image that failed with its reason. No results, no
// footer.
func Footer(results []Result) string {
	var paths, failed []string

	for _, r := range results {
		if r.Err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", r.URL, r.Err))
		} else {
			paths = append(paths, r.Path)
		}
	}

	var b strings.Builder

	if len(paths) > 0 {
		fmt.Fprintf(&b, "Images: %s - read each before summarising\n", strings.Join(paths, ", "))
	}

	if len(failed) > 0 {
		fmt.Fprintf(&b, "Images not downloaded: %s\n", strings.Join(failed, ", "))
	}

	return b.String()
}
