package asks

import (
	"regexp"
	"strconv"
	"strings"
)

// Shown is a question as the banner and the walk draw it: the stored one,
// or the one recovered from its prose. Stored is what an answer goes against.
type Shown struct {
	Question

	Stored    Question
	Recovered bool
	Context   string // background recovered from the question's prose
	Note      string // why the default is the default, from the prose
}

// ShownQuestions are a's questions as every view draws and asks them.
func ShownQuestions(a Ask) []Shown {
	qs := a.Questions
	if len(qs) == 0 {
		qs = Parse(a.Text)
	}

	out := make([]Shown, len(qs))
	for i, q := range qs {
		out[i] = Shown{Question: q, Stored: q}
		if r, ok := Recover(q); ok {
			out[i] = Shown{Question: r.Question, Stored: q, Recovered: true, Context: r.Context, Note: r.Note}
		}
	}

	return out
}

// ContextOf is the context a view prints above the first question: the
// ask's own, then question 1's recovered background, which reads as the
// ask's. A later question's background stays beside that question.
func ContextOf(a Ask, qs []Shown) []string {
	out := Sentences(a.Context)
	if len(qs) > 0 {
		out = append(out, Sentences(qs[0].Context)...)
	}

	return out
}

// labelled is a context sentence opening with a one-word label, "Log: ...".
var labelled = regexp.MustCompile(`^([A-Za-z][\w-]{0,11}):\s+(\S.*)$`)

// Labelled splits "Log: see the run." into its label and the rest, which
// the views draw as a row with the label in a column.
func Labelled(sentence string) (string, string, bool) {
	m := labelled.FindStringSubmatch(sentence)
	if m == nil {
		return "", "", false
	}

	return m[1], m[2], true
}

// Mark is what follows the default option: "(default)", with the reason
// recovered from the prose when there is one.
func Mark(q Shown) string {
	if q.Note != "" {
		return "(default, " + q.Note + ")"
	}

	return "(default)"
}

// ResolveReply maps a reply typed in the asking agent's pane to the answer
// the inbox would send: a letter, a number or an option's text for a lone
// question, "1. a 2. b" or one token per question for several. A numbered
// reply also answers a free-text question with its words, and a question
// with options it leaves out takes its default. Anything else stays the
// user's own words.
func ResolveReply(qs []Shown, reply string) (string, bool) {
	reply = strings.TrimSpace(reply)
	if reply == "" || len(qs) == 0 {
		return "", false
	}

	values, ok := numberedReply(reply)

	switch {
	case ok:
	case !allOptions(qs):
		// unnumbered, a free-text question's words cannot be told apart
		return "", false
	case len(qs) == 1:
		values = map[int]string{qs[0].N: reply}
	default:
		tokens := strings.FieldsFunc(reply, func(r rune) bool { return r == ' ' || r == ',' })
		if len(tokens) != len(qs) {
			return "", false
		}

		values = map[int]string{}
		for i, q := range qs {
			values[q.N] = tokens[i]
		}
	}

	answers := map[int]string{}
	stored := make([]Question, len(qs))

	for i, q := range qs {
		stored[i] = q.Stored

		v, given := values[q.N]
		if !given && len(q.Options) == 0 {
			// a free-text question has no default to fall back on
			return "", false
		}

		if !given {
			// a recovered default goes as its text, which the stored question takes
			answers[q.N] = q.Default()

			continue
		}

		opt, err := Resolve(q.Question, v)
		if err != nil {
			return "", false
		}

		answers[q.N] = opt
	}

	// a number the ask does not have is not a reply to it
	for n := range values {
		if _, known := answers[n]; !known {
			return "", false
		}
	}

	return Format(stored, answers), true
}

// numberedReply splits "1. a 2. b" by question number. It reports false
// unless the reply opens with a number and names none twice.
func numberedReply(reply string) (map[int]string, bool) {
	locs := numbered.FindAllStringSubmatchIndex(reply, -1)
	if len(locs) == 0 || locs[0][0] != 0 {
		return nil, false
	}

	out := map[int]string{}

	for i, loc := range locs {
		n, _ := strconv.Atoi(reply[loc[2]:loc[3]])

		end := len(reply)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}

		if _, dup := out[n]; dup {
			return nil, false
		}

		out[n] = strings.TrimSpace(reply[loc[1]:end])
	}

	return out, true
}
