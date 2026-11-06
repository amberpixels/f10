package main

import (
	"net/url"
	"path/filepath"
	"strings"
)

// fileLink links a markdown path to its file:// url, its text left as the
// path since people copy it; other files stay plain, as only markdown has
// a handler to open it.
func fileLink(path string) string {
	return hyperlink(fileURL(path), path)
}

// fileURL is the absolute file:// url of a markdown path, else empty.
func fileURL(path string) string {
	if !strings.HasSuffix(path, ".md") {
		return ""
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}

	return (&url.URL{Scheme: "file", Path: abs}).String()
}
