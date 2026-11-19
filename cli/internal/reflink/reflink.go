// Package reflink links the first mention of each task id in a document
// that renders links. It does no I/O: the caller resolves each id.
package reflink

import (
	"html"
	"regexp"
	"strings"
)

// A Mention is the first bare mention of an id in a document, at a 1-based
// line, and whether it was rewritten into a link.
type Mention struct {
	ID     string
	Line   int
	Linked bool
}

// Resolve gives an id's title and url; false leaves the mention bare.
type Resolve func(id string) (title, url string, ok bool)

// A Scan is Markdown or HTML.
type Scan func(text, prefix string, resolve Resolve) (string, []Mention)

// None resolves nothing, so a scan only reports the bare mentions.
func None(string) (string, string, bool) { return "", "", false }

// MarkdownLink is the task as a markdown link, brackets in the title escaped
// so the link text always ends where the link does.
func MarkdownLink(id, title, url string) string {
	title = strings.NewReplacer("[", `\[`, "]", `\]`).Replace(title)

	return "[" + id + ": " + title + "](" + url + ")"
}

// HTMLLink is the task as an anchor, its title and url escaped.
func HTMLLink(id, title, url string) string {
	return `<a href="` + html.EscapeString(url) + `">` + html.EscapeString(id+": "+title) + "</a>"
}

// Markdown links each id's first bare mention outside code, comments, links
// and the lines f10 parses (After:, trailers, closing keywords).
func Markdown(text, prefix string, resolve Resolve) (string, []Mention) {
	kinds := make([]kind, len(text))
	skip(kinds, text, commentRE)
	markLines(kinds, text)
	markCodeSpans(kinds, text)
	markLinks(kinds, text)

	return rewrite(text, prefix, kinds, resolve, MarkdownLink)
}

// HTML is Markdown for a page: it skips the head, script, style, pre and
// code elements and every tag's attributes, and counts an anchor as linked.
func HTML(text, prefix string, resolve Resolve) (string, []Mention) {
	kinds := make([]kind, len(text))
	skip(kinds, text, commentRE)
	for _, re := range rawElementREs {
		skip(kinds, text, re)
	}

	markAnchors(kinds, text)
	skip(kinds, text, tagRE)

	return rewrite(text, prefix, kinds, resolve, HTMLLink)
}

type kind uint8

const (
	plain  kind = iota
	exempt      // never rewritten and never a first mention
	linked      // already a link: the first mention, left as is
)

var (
	commentRE = regexp.MustCompile(`(?s)<!--.*?(?:-->|\z)`)
	anchorRE  = regexp.MustCompile(`(?is)<a\b[^>]*>(.*?)</a\s*>`)
	tagRE     = regexp.MustCompile(`<[A-Za-z/!][^>]*>`)

	// [text](url "title") and [text][ref], brackets escaped inside the text
	inlineLinkRE = regexp.MustCompile(`!?\[((?:\\.|[^\]\\])*)\](\([^)\s]*(?:\s+"[^"]*")?\)|\[[^\]]*\])`)
	urlRE        = regexp.MustCompile(`<?https?://[^\s<>()\[\]]+>?`)

	// Go's regexp has no backreference, so each element closes by name
	rawElementREs = func() []*regexp.Regexp {
		var out []*regexp.Regexp
		for _, el := range []string{"head", "script", "style", "pre", "code", "title"} {
			out = append(out, regexp.MustCompile(`(?is)<`+el+`\b.*?(?:</\s*`+el+`\s*>|\z)`))
		}

		return out
	}()

	fenceRE = regexp.MustCompile("^ {0,3}(```+|~~~+)")

	// the lines a parser reads: f10's After: and trailers, and the host's
	// closing keywords, which only work on the bare form; a keyword opens a
	// parsed line only before a reference, so prose starting "Fix the" is not one
	parsedLineRE = regexp.MustCompile(`(?i)^\s*(?:[-*]\s+)?(?:(?:after|task|step|review)\s*:|` +
		`(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?|part\s+of)\s+(?:[#!]\d|https?://|[a-z][a-z0-9]*-\d))`)
)

func skip(kinds []kind, text string, re *regexp.Regexp) {
	for _, m := range re.FindAllStringIndex(text, -1) {
		fill(kinds, m[0], m[1], exempt)
	}
}

// fill sets a plain range to k; exempt also overrides linked, since code
// inside a link's text is still code.
func fill(kinds []kind, from, to int, k kind) {
	for i := from; i < to; i++ {
		if kinds[i] == plain || k == exempt {
			kinds[i] = k
		}
	}
}

// markLines exempts fenced and indented code blocks and the parsed lines.
func markLines(kinds []kind, text string) {
	var (
		fence     string // the open fence's run, "" outside one
		indented  bool   // inside an indented code block
		prevBlank = true
		listAbove bool // the paragraph above the blank line was a list
	)

	for start := 0; start < len(text); {
		end := strings.IndexByte(text[start:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += start
		}

		line := text[start:end]
		blank := strings.TrimSpace(line) == ""

		switch {
		case fence != "":
			fill(kinds, start, end, exempt)

			if closesFence(line, fence) {
				fence = ""
			}
		case fenceRE.MatchString(line):
			fence = fenceRE.FindStringSubmatch(line)[1]
			fill(kinds, start, end, exempt)
		case !blank && isIndented(line) && (indented || (prevBlank && !listAbove)):
			indented = true

			fill(kinds, start, end, exempt)
		case parsedLineRE.MatchString(line):
			fill(kinds, start, end, exempt)
		}

		if !blank && !isIndented(line) {
			indented = false
			listAbove = isListItem(line)
		}

		prevBlank = blank
		start = end + 1
	}
}

// closesFence reports a line closing the fence: the same character, at least
// as long, and nothing after it.
func closesFence(line, fence string) bool {
	m := fenceRE.FindStringSubmatch(line)

	return len(m) == 2 && m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(line[len(m[0]):]) == ""
}

func isIndented(line string) bool {
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
}

var listItemRE = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s`)

func isListItem(line string) bool { return listItemRE.MatchString(line) }

// markCodeSpans exempts inline code: a run of backticks up to the next run
// of the same length; an unmatched run is literal.
func markCodeSpans(kinds []kind, text string) {
	for i := 0; i < len(text); {
		if text[i] != '`' || kinds[i] == exempt {
			i++

			continue
		}

		n := 0
		for i+n < len(text) && text[i+n] == '`' {
			n++
		}

		closeAt := -1

		for j := i + n; j < len(text) && kinds[j] != exempt; {
			if text[j] != '`' {
				j++

				continue
			}

			m := 0
			for j+m < len(text) && text[j+m] == '`' {
				m++
			}

			if m == n {
				closeAt = j + m

				break
			}

			j += m
		}

		if closeAt < 0 {
			i += n

			continue
		}

		fill(kinds, i, closeAt, exempt)
		i = closeAt
	}
}

// markLinks counts a markdown link's text, and an anchor's content, as
// linked, and exempts urls and tag attributes.
func markLinks(kinds []kind, text string) {
	for _, m := range inlineLinkRE.FindAllStringSubmatchIndex(text, -1) {
		if kinds[m[0]] == exempt {
			continue
		}

		fill(kinds, m[2], m[3], linked)
		fill(kinds, m[4], m[5], exempt)
	}

	markAnchors(kinds, text)
	skip(kinds, text, urlRE)
	skip(kinds, text, tagRE)
}

func markAnchors(kinds []kind, text string) {
	for _, m := range anchorRE.FindAllStringSubmatchIndex(text, -1) {
		if kinds[m[0]] != exempt {
			fill(kinds, m[2], m[3], linked)
		}
	}
}

// rewrite links the first plain mention of each id. An id already linked
// earlier, or one the resolver does not know, keeps its later mentions bare.
func rewrite(text, prefix string, kinds []kind, resolve Resolve, render func(id, title, url string) string,
) (string, []Mention) {
	if prefix == "" {
		return text, nil
	}

	var (
		out      strings.Builder
		mentions []Mention
		seen     = map[string]bool{}
		last     int
	)

	for _, m := range idRE(prefix).FindAllStringSubmatchIndex(text, -1) {
		from, to := m[2], m[3]
		if kinds[from] == exempt || !bounded(text, from, to) {
			continue
		}

		id := strings.ToUpper(prefix) + "-" + text[m[4]:m[5]]
		if seen[id] {
			continue
		}

		seen[id] = true

		if kinds[from] == linked {
			continue
		}

		mention := Mention{ID: id, Line: 1 + strings.Count(text[:from], "\n")}

		if title, url, ok := resolve(id); ok && title != "" && url != "" {
			out.WriteString(text[last:from])
			out.WriteString(render(id, title, url))

			last, mention.Linked = to, true
		}

		mentions = append(mentions, mention)
	}

	out.WriteString(text[last:])

	return out.String(), mentions
}

func idRE(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(` + regexp.QuoteMeta(prefix) + `-(\d+))`)
}

// bounded reports an id standing alone, not inside a path, a branch name,
// a file name or a bundle (`plans/FT-5.md`, `FT-5/fix`, `FT-5-x`, `FT-5+FT-6`).
func bounded(text string, from, to int) bool {
	if from > 0 && strings.ContainsRune("/_-.+\\#@", rune(text[from-1])) || from > 0 && isWord(text[from-1]) {
		return false
	}

	if to == len(text) {
		return true
	}

	next := text[to]

	switch {
	case isWord(next), next == '/', next == '+':
		return false
	case next == '.' || next == '-':
		return to+1 >= len(text) || !isWord(text[to+1])
	}

	return true
}

func isWord(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
