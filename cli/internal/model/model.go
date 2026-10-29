// Package model reads a claude model name as typed: a family, a family and a
// version, or a full id. Only the families are known here; versions change
// too often to keep a list, so a wrong one is passed on for claude to refuse.
package model

import (
	"strings"
	"unicode"
)

// Families are the claude model families --model may name. A full
// `claude-` id of a family missing here still passes as typed.
var Families = []string{"opus", "sonnet", "haiku", "fable"}

// maxTypos is how far a typed family may be from a real one and still be
// read as it.
const maxTypos = 2

// Resolve reads raw as a model name: the value to pass claude, a note
// when that differs from raw beyond case, and false when raw names no
// family.
func Resolve(raw string) (string, string, bool) {
	typed := strings.TrimSpace(raw)
	lower := strings.ToLower(typed)

	if lower == "" {
		return "", "", false
	}

	if strings.HasPrefix(lower, "claude-") {
		return typed, "", true
	}

	word, version, ok := split(lower)
	if !ok {
		return "", "", false
	}

	family, ok := nearest(word)
	if !ok {
		return "", "", false
	}

	value := family
	if len(version) > 0 {
		value = "claude-" + family + "-" + strings.Join(version, "-")
	}

	var note string
	if value != lower {
		note = "--model " + typed + " read as " + value
	}

	return value, note, true
}

// split cuts a name into its one run of letters and its runs of digits; a
// second run of letters, or any other character, is not a model name.
func split(s string) (string, []string, bool) {
	var (
		word    string
		version []string
		cur     []rune
		curKind int // 0 none, 1 letters, 2 digits
	)

	flush := func() bool {
		defer func() { cur, curKind = nil, 0 }()

		switch curKind {
		case 1:
			if word != "" {
				return false
			}

			word = string(cur)
		case 2:
			version = append(version, string(cur))
		}

		return true
	}

	for _, r := range s {
		kind := 0

		switch {
		case unicode.IsLetter(r):
			kind = 1
		case unicode.IsDigit(r):
			kind = 2
		case r == ' ' || r == '.' || r == '-' || r == '_':
		default:
			return "", nil, false
		}

		if kind != curKind && !flush() {
			return "", nil, false
		}

		if kind != 0 {
			cur, curKind = append(cur, r), kind
		}
	}

	if !flush() || word == "" {
		return "", nil, false
	}

	return word, version, true
}

// nearest is the family word is, or the single family within maxTypos of
// it; a tie is no match.
func nearest(word string) (string, bool) {
	best, bestDist, tie := "", maxTypos+1, false

	for _, f := range Families {
		d := distance(word, f)

		switch {
		case d < bestDist:
			best, bestDist, tie = f, d, false
		case d == bestDist:
			tie = true
		}
	}

	if best == "" || tie {
		return "", false
	}

	return best, true
}

// distance is the optimal string alignment distance: insertions,
// deletions, substitutions and adjacent swaps each cost one.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)

	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}

	for j := range d[0] {
		d[0][j] = j
	}

	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}

			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)

			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}

	return d[len(ra)][len(rb)]
}
