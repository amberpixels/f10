// Package trace is the vocabulary of what f10 leaves on the host for a reader
// of the history: the git trailers a step's commit carries. Render and Parse
// sit side by side so the lines written are the lines read back;
// docs/traces.md is the spec, testdata/trailers its conformance suite.
package trace

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The three trailer keys, version 1 of the spec.
const (
	KeyTask   = "Task"
	KeyStep   = "Step"
	KeyReview = "Review"
)

// Review is a resolve's fix commit in counts: the round it settled, and
// how many findings it fixed and skipped.
type Review struct {
	Round   int `json:"round"`
	Fixed   int `json:"fixed"`
	Skipped int `json:"skipped"`
}

// Trailers is what one commit message says about its task and step; Tasks
// lists a bundle lead first.
type Trailers struct {
	Tasks  []string `json:"tasks,omitempty"`
	Step   string   `json:"step,omitempty"`
	Review *Review  `json:"review,omitempty"`
}

// Parsed is what Parse reads out of a whole message: a squash merge
// concatenates commits, so every Step: and Review: line is kept in order,
// Step and Review being the last.
type Parsed struct {
	Trailers

	Steps   []string `json:"steps,omitempty"`
	Reviews []Review `json:"reviews,omitempty"`
}

// Zero reports a value with nothing to write.
func (t Trailers) Zero() bool {
	return len(t.Tasks) == 0 && t.Step == "" && t.Review == nil
}

var (
	// ErrBadStep is a step name outside `[a-z][a-z0-9-]*`.
	ErrBadStep = errors.New("trace: a step is a lowercase token")
	// ErrBadTask is a task id with whitespace in it.
	ErrBadTask = errors.New("trace: a task id has no whitespace")
	// ErrBadReview is a review count below zero.
	ErrBadReview = errors.New("trace: review counts are zero or more")
)

var stepRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Render writes the trailer lines in fixed order: every Task:, then Step:,
// then Review:, each newline-terminated. A zero value renders as "". It
// refuses a value Parse would not read back the same way.
func Render(t Trailers) (string, error) {
	var b strings.Builder

	for _, id := range t.Tasks {
		if id = strings.TrimSpace(id); id == "" || strings.ContainsAny(id, " \t\r\n") {
			return "", fmt.Errorf("%w: %q", ErrBadTask, id)
		}

		fmt.Fprintf(&b, "%s: %s\n", KeyTask, id)
	}

	if t.Step != "" {
		if !stepRe.MatchString(t.Step) {
			return "", fmt.Errorf("%w: %q", ErrBadStep, t.Step)
		}

		fmt.Fprintf(&b, "%s: %s\n", KeyStep, t.Step)
	}

	if r := t.Review; r != nil {
		if r.Round < 0 || r.Fixed < 0 || r.Skipped < 0 {
			return "", fmt.Errorf("%w: %+v", ErrBadReview, *r)
		}

		fmt.Fprintf(&b, "%s: round=%d fixed=%d skipped=%d\n", KeyReview, r.Round, r.Fixed, r.Skipped)
	}

	return b.String(), nil
}

// A trailer is any whole `Key: value` line, not anchored to the last
// paragraph: a squash merge buries a commit's trailers mid-body.
var lineRe = regexp.MustCompile(`(?i)^(task|step|review):[ \t]+(\S.*?)[ \t]*$`)

// Parse reads every trailer line. Nothing is an error: history nobody
// controlled is still read, and no trailer parses to a zero value.
func Parse(message string) Parsed {
	var p Parsed

	for line := range strings.SplitSeq(message, "\n") {
		m := lineRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}

		switch strings.ToLower(m[1]) {
		case "task":
			p.addTask(m[2])
		case "step":
			p.Steps = append(p.Steps, m[2])
			p.Step = m[2]
		case "review":
			p.Reviews = append(p.Reviews, parseReview(m[2]))
		}
	}

	if n := len(p.Reviews); n > 0 {
		p.Review = &p.Reviews[n-1]
	}

	return p
}

// addTask keeps one spelling per id: the first, compared case-insensitively.
func (p *Parsed) addTask(id string) {
	for _, have := range p.Tasks {
		if strings.EqualFold(have, id) {
			return
		}
	}

	p.Tasks = append(p.Tasks, id)
}

func parseReview(value string) Review {
	var r Review

	for pair := range strings.FieldsSeq(value) {
		key, raw, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}

		n, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}

		switch strings.ToLower(key) {
		case "round":
			r.Round = n
		case "fixed":
			r.Fixed = n
		case "skipped":
			r.Skipped = n
		}
	}

	return r
}
