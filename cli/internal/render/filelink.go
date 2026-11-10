package render

import (
	"net/url"
	"path/filepath"
	"strings"
)

// FileLink links a markdown path to its file:// url, its text left as the
// path since people copy it; other files stay plain, as only markdown has
// a handler to open it.
func FileLink(path string) string {
	return Hyperlink(FileURL(path), path)
}

// FileURL is the absolute file:// url of a markdown path, else empty.
func FileURL(path string) string {
	if !strings.HasSuffix(path, ".md") {
		return ""
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}

	return (&url.URL{Scheme: "file", Path: abs}).String()
}
