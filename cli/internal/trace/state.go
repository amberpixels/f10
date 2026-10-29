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

// State is the hidden block a PR body carries: where the task stands, as the
// last step left it. Round, Open and Asks are a review's counts (findings
// awaiting a verdict, questions waiting on a person); the JSON key order is
// the struct's, which is the spec's.
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

// blockRe matches up to the first `}` a closing marker follows, so a block
// holds no nested object; stateSpans says which matches are the state.
var blockRe = regexp.MustCompile(`<!--[ \t]*state[ \t]+(\{.*?\})[ \t]*-->`)

// ParseState reads the first block a body holds. Anything unreadable (no
// block, bad JSON, another version, a count below zero) is no state, never an
// error: a body nobody controlled is still read.
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

	// carrying a count RenderState refuses would refuse every rewrite after
	if s.Round < 0 || s.Open < 0 || s.Asks < 0 {
		return State{}, false
	}

	return s, true
}

// stateSpans skips blocks inside a fence or a backtick span: those are
// examples, as the spec's own is, and a PR body quoting the spec must keep it.
func stateSpans(body string) [][]int {
	var spans [][]int

	for _, span := range blockRe.FindAllStringIndex(body, -1) {
		if !inCode(body, span[0]) {
			spans = append(spans, span)
		}
	}

	return spans
}

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

// Merge is the state a rewrite leaves: the update's fields, and each count
// from the update where passed, else from prev.
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

// RenderState writes the block for s, one line, V forced to StateVersion. It
// refuses a value ParseState would not read back the same way.
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

func badID(id string) bool {
	return strings.TrimSpace(id) != id || strings.ContainsAny(id, " \t\r\n") || strings.Contains(id, "--")
}

// PutState replaces the first block in place and removes any later one, else
// appends after a blank line; nothing else in the body changes.
func PutState(body string, s State) (string, error) {
	block, err := RenderState(s)
	if err != nil {
		return "", err
	}

	return putBlock(body, block), nil
}

// CarryState copies from's block into body byte for byte, where PutState
// would put one, so a body rewritten from scratch with traces off keeps the
// old block unrewritten.
func CarryState(body, from string) string {
	spans := stateSpans(from)
	if len(spans) == 0 {
		return body
	}

	return putBlock(body, from[spans[0][0]:spans[0][1]])
}

func putBlock(body, block string) string {
	spans := stateSpans(body)
	if len(spans) == 0 {
		if strings.TrimSpace(body) == "" {
			return block + "\n"
		}

		return strings.TrimRight(body, " \t\r\n") + "\n\n" + block + "\n"
	}

	// later blocks first, so the earlier offsets stay valid
	for _, span := range slices.Backward(spans[1:]) {
		body = removeSpan(body, span[0], span[1])
	}

	return body[:spans[0][0]] + block + body[spans[0][1]:]
}

// Rewrite is what a step does to a PR body: Merge over what it holds, put back.
func Rewrite(body string, u Update) (string, error) {
	prev, _ := ParseState(body)

	return PutState(body, Merge(prev, u))
}

// StripState removes every block, for a commit message built from a PR body:
// the block describes the PR, not the commit.
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
