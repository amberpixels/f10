package hostops

import (
	"strings"

	"github.com/amberpixels/f10/cli/internal/mdline"
)

// AfterIn is the id the body's After: line names, or "": bare or as a link
// to the task, with emphasis or code marks around either part ignored.
func AfterIn(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if id := AfterID(line); id != "" {
			return id
		}
	}

	return ""
}

// AfterID is the id one line names as an After: line, or "".
func AfterID(line string) string {
	line = strings.TrimRight(line, "\r")
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return ""
	}

	key, value, ok := mdline.Cut(line, ":")
	if !ok || !strings.EqualFold(key, "after") {
		return ""
	}

	id := mdline.Bare(value)

	// the link's text, never its url, names the id, so a moved task still
	// names its base
	if text, ok := strings.CutPrefix(id, "["); ok && strings.Contains(text, "](") {
		text, url, _ := strings.Cut(text, "](")
		if !strings.HasSuffix(url, ")") || strings.ContainsAny(url, " \t") {
			return ""
		}

		text, _, _ = strings.Cut(text, ":")
		id = strings.TrimSpace(text)
	}

	if id == "" || strings.ContainsAny(id, " \t[]") {
		return ""
	}

	return id
}
