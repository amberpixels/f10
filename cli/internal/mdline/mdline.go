// Package mdline reads the `key: value` lines f10 takes out of markdown
// that people and agents edit: project.md's facts and a task body's After:
// line. The model reads `**local**: ...` and `local: ...` alike, so every
// Go reader goes through here to read them alike too.
package mdline

import (
	"regexp"
	"strings"
)

// marks are markdown's emphasis and code characters, which may wrap a key
// or a value without changing what it says.
const marks = "*_`"

// Item strips a list bullet ("- ", "* ", "+ ") and the space around the
// line; ok is false when the line is not a list item.
func Item(line string) (string, bool) {
	line = strings.TrimSpace(line)
	for _, bullet := range []string{"- ", "* ", "+ "} {
		if rest, ok := strings.CutPrefix(line, bullet); ok {
			return strings.TrimSpace(rest), true
		}
	}

	return line, false
}

// Cut splits a line at the earliest of seps. The key comes back without
// marks; the value keeps its code spans, since some values carry an
// identity in one, and loses only the emphasis a marked key left behind
// (`**local:** ...`). A bold label cuts after its closing `**` even with no
// separator (`**Reviewer** bot ...`). It returns the key, the value, and
// whether a non-empty key was found.
func Cut(line string, seps ...string) (string, string, bool) {
	if rest, bold := strings.CutPrefix(line, "**"); bold {
		label, after, closed := strings.Cut(rest, "**")
		after = strings.TrimSpace(after)

		// the label is the key only when a separator follows it at once, or
		// none comes at all; `**direct** (planless): ...` cuts at its colon
		if closed && !containsAny(label, seps) && (startsWithSep(after, seps) || !containsAny(after, seps)) {
			return Bare(label), trimSep(after, seps), Bare(label) != ""
		}
	}

	at, width := -1, 0
	for _, sep := range seps {
		if i := strings.Index(line, sep); i >= 0 && (at < 0 || i < at) {
			at, width = i, len(sep)
		}
	}

	if at < 0 {
		return "", "", false
	}

	key := Bare(Unmark(line[:at]))

	return key, trimLeftover(strings.TrimSpace(line[at+width:])), key != ""
}

// Bare trims marks and space from both ends: `**FT-12**` is FT-12.
func Bare(s string) string {
	return strings.Trim(s, marks+" \t")
}

// Unmark drops every emphasis star and code mark, for a phrase matched
// inside a paragraph rather than at the start of a line.
func Unmark(s string) string {
	return strings.NewReplacer("*", "", "`", "").Replace(s)
}

// Prose drops fenced blocks and inline code spans, each leaving a space,
// for a phrase that must come from the prose and not from a command or a
// name quoted in code. A backtick with no closing one on its line stays.
func Prose(s string) string {
	lines := strings.Split(s, "\n")
	fence := "" // the open fence's character run, closed only by the same one

	for i, line := range lines {
		t := strings.TrimSpace(line)

		switch {
		case fence != "":
			if strings.HasPrefix(t, fence) {
				fence = ""
			}

			lines[i] = " "
		case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
			fence = t[:3]
			lines[i] = " "
		default:
			lines[i] = codeSpanRe.ReplaceAllString(line, " ")
		}
	}

	return strings.Join(lines, "\n")
}

var codeSpanRe = regexp.MustCompile("`[^`]*`")

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}

	return false
}

func startsWithSep(s string, seps []string) bool {
	for _, sep := range seps {
		if strings.HasPrefix(s, strings.TrimSpace(sep)) {
			return true
		}
	}

	return false
}

// trimSep drops a separator that follows a bold label: `**Reviewer** - bot`.
func trimSep(value string, seps []string) string {
	for _, sep := range seps {
		if rest, ok := strings.CutPrefix(value, strings.TrimSpace(sep)); ok {
			return strings.TrimSpace(rest)
		}
	}

	return value
}

// trimLeftover drops a run of emphasis marks standing alone at the start
// of a value, the closing half of a marked key: `** implement`.
func trimLeftover(value string) string {
	run := strings.TrimLeft(value, "*_")
	if run == value || run == "" || run[0] == ' ' || run[0] == '\t' {
		return strings.TrimSpace(run)
	}

	return value
}
