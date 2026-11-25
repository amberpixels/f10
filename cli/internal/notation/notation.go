// Package notation is how f10 start's command line spells an entry:
// <task>[-suffix][@<base>...][:profile], a bundle's tasks joined by +, and a
// comma stacking an entry on the one before it. The parser and the
// proposal's printer both go through it, so the two cannot drift. It reads
// nothing.
package notation

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/taskid"
)

// The marks of the notation.
const (
	Bundle  = "+" // joins a bundle's tasks, lead first
	Chain   = "," // stacks an entry on the one before it
	Base    = "@" // names one base
	Profile = ":" // opens the agent profile, which comes last
)

// An Entry is one task or bundle of the command line.
type Entry struct {
	// Task is the task as typed, its -suffix kept: `A-1`, `A-1-x`, `A-1+A-2`
	// or a url. It is kept whole because a batch's record is keyed on the
	// command line as typed.
	Task    string
	Bases   []string // each base as typed: the entry before it in a chain, then each @
	Profile string
}

// Members is each task of the entry, lead first, the lead's suffix cut off.
func (e Entry) Members() []string {
	id, _ := SplitSuffix(e.Task)
	if IsURL(id) {
		return []string{id}
	}

	return strings.Split(id, Bundle)
}

// ID is the entry without its suffix: what another entry names as its base.
func (e Entry) ID() string {
	id, _ := SplitSuffix(e.Task)

	return id
}

// Suffix is the entry's -suffix, "" for none. A bundle takes none.
func (e Entry) Suffix() string {
	_, suffix := SplitSuffix(e.Task)

	return suffix
}

// Ordered reports a typed base on any entry.
func Ordered(entries []Entry) bool {
	return slices.ContainsFunc(entries, func(e Entry) bool { return len(e.Bases) > 0 })
}

// Parse reads one command-line token as a chain of entries. A comma stacks
// an entry on the one before it, and each @ names one more base. A url is
// one entry taken whole, because its path or query may hold either mark.
func Parse(tok string) ([]Entry, error) {
	if IsURL(tok) {
		task, p, err := SplitProfile(tok)
		if err != nil {
			return nil, err
		}

		if urlOrdered(task) {
			return nil, fmt.Errorf("%s: a url takes no order: use the task's id, as in A,B or C@A", tok)
		}

		return []Entry{{Task: task, Profile: p}}, nil
	}

	var (
		out  []Entry
		prev string
	)

	for link := range strings.SplitSeq(tok, Chain) {
		if link == "" {
			return nil, fmt.Errorf("%s: an empty entry beside a comma: A,B stacks B on A", tok)
		}

		head, p, err := SplitProfile(link)
		if err != nil {
			return nil, err
		}

		if prof, bases, ok := strings.Cut(p, Base); ok {
			return nil, fmt.Errorf("%s: the profile comes last: %s@%s:%s", link, head, bases, prof)
		}

		parts := strings.Split(head, Base)
		if parts[0] == "" {
			return nil, fmt.Errorf("%s names no task before @: C@A", link)
		}

		var bases []string
		if prev != "" {
			bases = append(bases, prev)
		}

		for _, b := range parts[1:] {
			switch _, suffix := SplitSuffix(b); {
			case b == "":
				return nil, fmt.Errorf("%s: an @ names no base: C@A", link)
			case suffix != "":
				return nil, fmt.Errorf("%s: a base is a task id, with no -%s", link, suffix)
			case slices.ContainsFunc(bases, func(x string) bool { return strings.EqualFold(x, b) }):
				return nil, fmt.Errorf("%s names %s as a base twice", link, b)
			}

			bases = append(bases, b)
		}

		e := Entry{Task: parts[0], Profile: p, Bases: bases}
		prev = e.ID()
		out = append(out, e)
	}

	return out, nil
}

// Format spells one entry, every base as an @.
func Format(e Entry) string {
	var b strings.Builder

	b.WriteString(e.Task)

	for _, base := range e.Bases {
		b.WriteString(Base)
		b.WriteString(base)
	}

	if e.Profile != "" {
		b.WriteString(Profile)
		b.WriteString(e.Profile)
	}

	return b.String()
}

// FormatChain spells entries as one token, each stacked on the one before
// it. The comma says that base, so an entry's first base is left out when it
// is the entry before it, and Parse puts it back.
func FormatChain(entries []Entry) string {
	links := make([]string, 0, len(entries))

	for i, e := range entries {
		if i > 0 && len(e.Bases) > 0 && strings.EqualFold(e.Bases[0], entries[i-1].ID()) {
			e.Bases = e.Bases[1:]
		}

		links = append(links, Format(e))
	}

	return strings.Join(links, Chain)
}

// SplitProfile cuts a task token's :profile off: the first colon whose
// remainder holds no slash, so `FT-1:claude:opus:high` splits after the id,
// and a url's scheme and port stay part of the task.
func SplitProfile(tok string) (string, string, error) {
	for i := range len(tok) {
		if tok[i] != ':' || strings.Contains(tok[i+1:], "/") {
			continue
		}

		if tok[i+1:] == "" {
			return "", "", fmt.Errorf("%s names no profile after the colon: GH-12:opus", tok)
		}

		return tok[:i], tok[i+1:], nil
	}

	return tok, "", nil
}

// suffixRE is a reference salted with a word (`1454-attempt2`, `GH-1_v2`):
// f10 start salts the branch so a second worktree for one task can live
// beside the first.
var suffixRE = regexp.MustCompile(`^(` + taskid.Reference + `)[-_]([A-Za-z0-9][A-Za-z0-9_-]*)$`)

// SplitSuffix separates a salted reference into the reference and its
// suffix. A token without one, or a url, comes back whole with "".
func SplitSuffix(token string) (string, string) {
	token = strings.TrimSpace(token)
	if IsURL(token) {
		return token, ""
	}

	if m := suffixRE.FindStringSubmatch(token); m != nil {
		return m[1], m[4]
	}

	return token, ""
}

// JoinSuffix is SplitSuffix's inverse: id salted with suffix, or id alone
// with none.
func JoinSuffix(id, suffix string) string {
	if suffix == "" {
		return id
	}

	return id + "-" + suffix
}

// IsURL reports a token that is a url, which no mark splits.
func IsURL(token string) bool { return strings.Contains(token, "://") }

var idShape = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9]*-)?\d+$`)

// IsID says whether word is shaped as a task f10 start takes: `ABC-12` or a
// bare number with an optional -suffix, or a bundle of them. The shape is
// enough where a caller resolves each id against the project later.
func IsID(word string) bool {
	if word == "" || strings.HasPrefix(word, "-") {
		return false
	}

	for m := range strings.SplitSeq(word, Bundle) {
		id, _ := SplitSuffix(m)
		if !idShape.MatchString(id) {
			return false
		}
	}

	return true
}

// urlOrdered reports a url with an order typed around it: a mark before its
// scheme, or a mark and a task id at its end. Any other mark is the url's own.
func urlOrdered(u string) bool {
	scheme, _, _ := strings.Cut(u, "://")
	if strings.ContainsAny(scheme, Chain+Base) {
		return true
	}

	i := strings.LastIndexAny(u, Chain+Base)

	return i >= 0 && IsID(u[i+1:])
}
