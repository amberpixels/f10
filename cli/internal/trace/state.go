package trace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// StateVersion is the `v` every block written today carries; a block with
// another version reads as no state.
const StateVersion = 1

// State is the hidden block a PR body carries: where the task stands now,
// as the last step that ran left it. Task is the lead's id, Riders the
// rest of a bundle, After the base task's id. Round, Open and Asks are a
// review's counts: the round, the findings awaiting a verdict, and the
// questions waiting on a person outside the run's own turn. The JSON key
// order is the struct's, which is the spec's.
type State struct {
	V      int      `json:"v"`
	Task   string   `json:"task,omitempty"`
	Riders []string `json:"riders,omitempty"`
	Step   string   `json:"step"`
	Round  int      `json:"round,omitempty"`
	Open   int      `json:"open,omitempty"`
	Asks   int      `json:"asks,omitempty"`
	After  string   `json:"after,omitempty"`
}

// Update is what one rewrite changes. Step, Task, Riders and After are
// set every time; a count left nil keeps the value the block already holds,
// and a count set to zero clears it.
type Update struct {
	Step   string
	Task   string
	Riders []string
	After  string
	Round  *int
	Open   *int
	Asks   *int
}

var (
	// ErrBadCount is a state count below zero.
	ErrBadCount = errors.New("trace: state counts are zero or more")
	// ErrBadAfter is a base task id with whitespace in it.
	ErrBadAfter = errors.New("trace: a base task id has no whitespace")
)

// A block is one HTML comment on a line of its own: `<!-- state {...} -->`.
// The JSON is matched up to the first `}` that a closing marker follows,
// so a block holds no nested object. Not every match is the state: see
// stateSpans.
var blockRe = regexp.MustCompile(`<!--[ \t]*state[ \t]+(\{.*?\})[ \t]*-->`)

// ParseState reads the first block a body holds. No block, JSON that does
// not parse, a version other than StateVersion, or a count below zero is
// no state, never an error: a body nobody controlled is still read.
// Unknown keys are ignored.
func ParseState(body string) (State, bool) {
	spans := stateSpans(body)
	if len(spans) == 0 {
		return State{}, false
	}

	m := blockRe.FindStringSubmatch(body[spans[0][0]:spans[0][1]])

	var s State
	if err := json.Unmarshal([]byte(m[1]), &s); err != nil || s.V != StateVersion {
		return State{}, false
	}

	// a count below zero is a block nobody rendered: RenderState would
	// refuse it, and carrying it would refuse every rewrite after
	if s.Round < 0 || s.Open < 0 || s.Asks < 0 {
		return State{}, false
	}

	return s, true
}

// stateSpans is every block in body that is the state and not text: one
// inside a fenced code block or a backtick span is an example, as the
// spec's own is, and a PR body quoting the spec must keep it.
func stateSpans(body string) [][]int {
	var spans [][]int

	for _, span := range blockRe.FindAllStringIndex(body, -1) {
		if !inCode(body, span[0]) {
			spans = append(spans, span)
		}
	}

	return spans
}

// inCode reports whether offset at sits inside a ``` or ~~~ fence, or
// after an odd number of backticks on its own line.
func inCode(body string, at int) bool {
	lineStart := strings.LastIndexByte(body[:at], '\n') + 1

	fenced := false

	for line := range strings.SplitSeq(body[:lineStart], "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
		}
	}

	return fenced || strings.Count(body[lineStart:at], "`")%2 == 1
}

// Merge is the state a rewrite leaves: the update's step, task, riders and
// after, and each count from the update where it was passed, else from
// prev. A prev that was no state is the zero value, so nothing carries.
func Merge(prev State, u Update) State {
	next := State{
		V:      StateVersion,
		Task:   u.Task,
		Riders: u.Riders,
		Step:   u.Step,
		Round:  prev.Round,
		Open:   prev.Open,
		Asks:   prev.Asks,
		After:  u.After,
	}

	if u.Round != nil {
		next.Round = *u.Round
	}

	if u.Open != nil {
		next.Open = *u.Open
	}

	if u.Asks != nil {
		next.Asks = *u.Asks
	}

	return next
}

// RenderState writes the block for s, one line, no trailing newline, with
// V set to StateVersion. It refuses a value ParseState would not read back
// the same way, or that would break the comment.
func RenderState(s State) (string, error) {
	s.V = StateVersion

	if !stepRe.MatchString(s.Step) {
		return "", fmt.Errorf("%w: %q", ErrBadStep, s.Step)
	}

	for _, id := range append([]string{s.Task}, s.Riders...) {
		if id != "" && badID(id) {
			return "", fmt.Errorf("%w: %q", ErrBadTask, id)
		}
	}

	if s.After != "" && badID(s.After) {
		return "", fmt.Errorf("%w: %q", ErrBadAfter, s.After)
	}

	if s.Round < 0 || s.Open < 0 || s.Asks < 0 {
		return "", fmt.Errorf("%w: %+v", ErrBadCount, s)
	}

	// json.Marshal would escape < and > inside strings, which is harmless
	// but not what the spec shows
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(s); err != nil {
		return "", err
	}

	return "<!-- state " + strings.TrimSpace(b.String()) + " -->", nil
}

// badID reports an id with whitespace, or one that would end the comment.
func badID(id string) bool {
	return strings.TrimSpace(id) != id || strings.ContainsAny(id, " \t\r\n") || strings.Contains(id, "--")
}

// PutState writes s into body: the first block is replaced where it is and
// any later one removed; a body without one gets the block appended after
// a blank line. Nothing else in the body changes.
func PutState(body string, s State) (string, error) {
	block, err := RenderState(s)
	if err != nil {
		return "", err
	}

	spans := stateSpans(body)
	if len(spans) == 0 {
		if strings.TrimSpace(body) == "" {
			return block + "\n", nil
		}

		return strings.TrimRight(body, " \t\r\n") + "\n\n" + block + "\n", nil
	}

	// later blocks first, so the earlier offsets stay valid
	for _, span := range slices.Backward(spans[1:]) {
		body = removeSpan(body, span[0], span[1])
	}

	return body[:spans[0][0]] + block + body[spans[0][1]:], nil
}

// Rewrite is PutState of Merge over the state body already holds: what a
// step does to a PR body.
func Rewrite(body string, u Update) (string, error) {
	prev, _ := ParseState(body)

	return PutState(body, Merge(prev, u))
}

// StripState removes every block from body, each with the line it had to
// itself and the blank line above it, for a commit message built from a
// PR body: the block describes the PR, not the commit.
func StripState(body string) string {
	for _, span := range slices.Backward(stateSpans(body)) {
		body = removeSpan(body, span[0], span[1])
	}

	return body
}

// removeSpan cuts body[start:end]. A block alone on its line takes the
// line with it, and one blank line before it, so no double blank remains.
func removeSpan(body string, start, end int) string {
	lineStart := strings.LastIndexByte(body[:start], '\n') + 1

	lineEnd := len(body)
	if i := strings.IndexByte(body[end:], '\n'); i >= 0 {
		lineEnd = end + i + 1
	}

	if strings.TrimSpace(body[lineStart:start]) != "" || strings.TrimSpace(body[end:lineEnd]) != "" {
		return body[:start] + body[end:]
	}

	before, rest := body[:lineStart], body[lineEnd:]

	switch {
	case before == "":
		// the body opened with the block: the blank line under it goes instead
		if strings.HasPrefix(rest, "\r\n") {
			rest = rest[2:]
		} else {
			rest = strings.TrimPrefix(rest, "\n")
		}
	case strings.HasSuffix(before, "\r\n\r\n"):
		before = before[:len(before)-2]
	case strings.HasSuffix(before, "\n\n"):
		before = before[:len(before)-1]
	}

	return before + rest
}
