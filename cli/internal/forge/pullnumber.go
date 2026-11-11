package forge

import (
	"path"
	"strings"
)

const (
	prSigil = "#"
	mrSigil = "!"
)

// PullNumber is a request's url as its host writes the number, for a caller
// holding only the url: a GitLab merge request's path names it, so `!12`,
// else `#128`.
func PullNumber(url string) string {
	if strings.Contains(url, "/merge_requests/") {
		return mrSigil + path.Base(url)
	}

	return prSigil + path.Base(url)
}
