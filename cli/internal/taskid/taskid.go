// Package taskid is the grammar of a task id: a prefix of letters and
// digits, a dash, a number. Every reader builds its pattern from these
// fragments, so they differ only where their input does.
package taskid

import "regexp"

// The prefix as three readers see it. Typed is what a user types as a
// reference, any case. Word is the first word of a skill argument, where a
// prefix holding a digit counts only upper-case, so `x86-64` opening a
// description stays a description. Declared is the format a project
// declares and `f10 init` writes back, upper-case and two to ten long.
const (
	Typed    = `[A-Za-z][A-Za-z0-9]*`
	Word     = `(?:[A-Za-z]+|[A-Z][A-Z0-9]*)`
	Declared = `[A-Z][A-Z0-9]{1,9}`
)

// Reference is a typed task reference: a bare number, `#123`, or a
// prefixed id in any case, the prefix and the number captured.
const Reference = `(?:(` + Typed + `)-)?#?(\d+)`

var (
	referenceRE = regexp.MustCompile(`^` + Reference + `$`)
	wordRE      = regexp.MustCompile(`^` + Word + `-\d+$`)
	numberRE    = regexp.MustCompile(`^\d+$`)
	declaredRE  = regexp.MustCompile(`^` + Declared + `$`)
)

// Parse splits a typed reference into its prefix, "" when bare, and number.
func Parse(token string) (string, string, bool) {
	m := referenceRE.FindStringSubmatch(token)
	if m == nil {
		return "", "", false
	}

	return m[1], m[2], true
}

// IsWord reports a prefixed id as the first word of a skill argument reads it.
func IsWord(token string) bool { return wordRE.MatchString(token) }

// IsNumber reports a bare issue number.
func IsNumber(token string) bool { return numberRE.MatchString(token) }

// ValidPrefix reports a prefix a project may declare.
func ValidPrefix(prefix string) bool { return declaredRE.MatchString(prefix) }
