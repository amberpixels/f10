package asks

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Recovered is a question whose choices were written as prose, laid out as
// the walk shows it: its background, the question with lettered options
// (the recommended one first, as the default) and that option's note.
type Recovered struct {
	Context  string
	Question Question
	Note     string
}

const letters = "abcdefghijklmnopqrstuvwxyz"

var (
	// an item marker: 1) (1) 1. a) (a), after a start, a space or a separator
	itemMarker = regexp.MustCompile(`(?:^|[\s,;])(\(?)([1a])([.)])\s`)
	listLabel  = regexp.MustCompile(`(?i)\b(?:options?|choices?)\s*:\s*$`)
	recommend  = regexp.MustCompile(`(?i)\s*\((recommended\b[^)]*)\)|\s+[-,]?\s*(recommended\b.*)$`)
	joiner     = regexp.MustCompile(`(?i)[\s,;.]+(?:or|and)?$`)
)

// Recover finds the choices a question without options lists in its text,
// "Options: 1) x, 2) y" or "(a) x (b) y", so the walk can offer them. It
// recovers every shape Lint warns about, and the stored ask stays as it was.
func Recover(q Question) (Recovered, bool) {
	if len(q.Options) > 0 {
		return Recovered{}, false
	}

	for _, loc := range itemMarker.FindAllStringSubmatchIndex(q.Text, -1) {
		start := loc[2]
		if start < 0 || loc[2] == loc[3] {
			start = loc[4]
		}

		items, ok := listFrom(q.Text[start:], q.Text[loc[2]:loc[3]] != "", q.Text[loc[4]], q.Text[loc[6]])
		if !ok {
			continue
		}

		return recovered(q.N, strings.TrimSpace(listLabel.ReplaceAllString(q.Text[:start], "")), items), true
	}

	return Recovered{}, false
}

// listFrom splits text, which opens with item 1 (or a), into its items:
// each next marker in the same form, counting up, the last running to the
// end. A last item asking something is prose after the list, not a choice.
func listFrom(text string, paren bool, first, closer byte) ([]string, bool) {
	marker := func(i int) string {
		m := strconv.Itoa(i+1) + string(closer)
		if first == 'a' {
			m = string(letters[min(i, len(letters)-1)]) + string(closer)
		}

		if paren {
			m = "(" + m
		}

		return m
	}

	var items []string

	rest := text[len(marker(0)):]

	for i := 1; ; i++ {
		next := regexp.MustCompile(`(?:^|[\s,;])` + regexp.QuoteMeta(marker(i)) + `\s`).FindStringIndex(rest)
		if next == nil || (first == 'a' && i >= len(letters)) {
			items = append(items, rest)

			break
		}

		items = append(items, rest[:next[0]])
		rest = strings.TrimLeft(rest[next[0]:], " ,;")[len(marker(i)):]
	}

	if len(items) < 2 || strings.HasSuffix(strings.TrimSpace(items[len(items)-1]), "?") {
		return nil, false
	}

	for i, it := range items {
		items[i] = strings.TrimSpace(joiner.ReplaceAllString(strings.TrimSpace(it), ""))
		if items[i] == "" {
			return nil, false
		}
	}

	return items, true
}

func recovered(n int, prose string, items []string) Recovered {
	r := Recovered{Question: Question{N: n, Text: prose}}

	// the question is the prose from its last sentence asking something;
	// what comes before is background
	sentences := Sentences(prose)
	for i, s := range slices.Backward(sentences) {
		if strings.HasSuffix(s, "?") {
			r.Context = strings.Join(sentences[:i], " ")
			r.Question.Text = strings.Join(sentences[i:], " ")

			break
		}
	}

	def := 0

	for i, it := range items {
		m := recommend.FindStringSubmatchIndex(it)
		if m == nil {
			continue
		}

		var note string
		if m[2] >= 0 {
			note = it[m[2]:m[3]]
		} else {
			note = it[m[4]:m[5]]
		}

		items[i] = strings.TrimSpace(joiner.ReplaceAllString(it[:m[0]]+it[m[1]:], ""))
		if r.Note == "" {
			def, r.Note = i, note
		}
	}

	// Default is Options[0], so the recommended item leads
	r.Question.Options = append([]string{items[def]}, append(items[:def:def], items[def+1:]...)...)

	return r
}

// Sentences splits text after a ., ? or ! that a space and an upper-case
// letter or a digit follow, so "e.g. this" and "v0.68.0" stay whole.
func Sentences(text string) []string {
	var (
		out   []string
		start int
	)

	text = strings.TrimSpace(text)

	for i := range len(text) - 1 {
		if !strings.ContainsRune(".?!", rune(text[i])) || text[i+1] != ' ' {
			continue
		}

		j := i + 1
		for j < len(text) && text[j] == ' ' {
			j++
		}

		if j < len(text) && (unicode.IsUpper(rune(text[j])) || unicode.IsDigit(rune(text[j]))) {
			out = append(out, strings.TrimSpace(text[start:i+1]))
			start = j
		}
	}

	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}

	return out
}
