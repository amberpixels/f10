// Package tasktitle is tracker.title: the template a task's title is
// written through on the tracker (`{id}: {title}`), and the strip that
// takes a leading id back off, so f10's own output never shows it twice.
package tasktitle

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Template is a declared tracker.title; the zero value is none, which
// renders and strips nothing.
type Template string

var placeholder = regexp.MustCompile(`\{[^{}]*\}`)

// Parse refuses a template without exactly one {title} and a placeholder
// other than {id}, {n} and {title}.
func Parse(raw string) (Template, error) {
	titles := 0

	for _, p := range placeholder.FindAllString(raw, -1) {
		switch p {
		case "{title}":
			titles++
		case "{id}", "{n}":
		default:
			return "", fmt.Errorf("%q: %s is not one of {id}, {n}, {title}", raw, p)
		}
	}

	if titles != 1 {
		return "", fmt.Errorf("%q: needs {title} exactly once", raw)
	}

	return Template(raw), nil
}

// Render is title written through the template for the task id and its
// number n. A title holding a placeholder's text is not substituted again.
func (t Template) Render(id, n, title string) string {
	if t == "" {
		return title
	}

	return strings.NewReplacer("{id}", id, "{n}", n, "{title}", title).Replace(string(t))
}

// Strip takes a leading id, and the punctuation and whitespace after it, off
// title. It strips only when the template opens with {id}, {n} or #{n}: an id
// inside a title is never a prefix f10 wrote. A title that is the id alone
// stays whole.
func (t Template) Strip(title, id, n string) string {
	var tokens []string

	switch {
	case strings.HasPrefix(string(t), "{id}"):
		tokens = []string{id}
	case strings.HasPrefix(string(t), "{n}"), strings.HasPrefix(string(t), "#{n}"):
		tokens = []string{"#" + n, n}
	default:
		return title
	}

	for _, token := range tokens {
		if token == "" || token == "#" || len(title) <= len(token) || !strings.EqualFold(title[:len(token)], token) {
			continue
		}

		rest := title[len(token):]

		// FT-1 is not the start of FT-12
		if r, _ := utf8.DecodeRuneInString(rest); unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}

		if rest = strings.TrimLeftFunc(rest, separator); rest != "" {
			return rest
		}
	}

	return title
}

// Applied reports whether title is already written through the template,
// so a retitle leaves it alone.
func (t Template) Applied(title, id, n string) bool {
	return t.Render(id, n, t.Strip(title, id, n)) == title
}

// separator is the punctuation and space between a prefix and the title;
// an opening bracket or quote starts the title itself.
func separator(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("([{\"'`", r)
}
