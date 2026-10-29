// Package asks keeps the questions driven agents stop on in one per-user
// inbox, so the user can answer from anywhere. An ask is a file: open while
// it waits, moved to answered/ by the first answer from any way in.
package asks

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// An Ask is one question set a driven agent, or a drive's finish, stopped on.
type Ask struct {
	Task      string     `yaml:"task"`
	URL       string     `yaml:"url,omitempty"`
	Root      string     `yaml:"root"`              // the storage root
	Main      string     `yaml:"main,omitempty"`    // the main checkout, where a drive is resumed from
	Project   string     `yaml:"project,omitempty"` // the main checkout's basename, for display
	Worktree  string     `yaml:"worktree"`          // the asking agent's checkout
	Suffix    string     `yaml:"suffix,omitempty"`  // the agent's name suffix, for a second attempt at a task
	Drive     int        `yaml:"drive,omitempty"`   // the drive whose task asks, 0 for none
	Skill     string     `yaml:"skill,omitempty"`   // where it asked; finish for finish's own question
	Text      string     `yaml:"text"`              // the ask as written, one line
	Questions []Question `yaml:"questions"`
	AskedAt   time.Time  `yaml:"asked_at"`

	Answer     string    `yaml:"answer,omitempty"` // what the agent, or finish, receives
	By         string    `yaml:"by,omitempty"`     // which way in answered it
	AnsweredAt time.Time `yaml:"answered_at,omitempty"`
	// DeliveredAt is when the answer reached the agent: at once when typed in
	// its pane or prompted by the answering command, else when a drive sends it
	DeliveredAt time.Time `yaml:"delivered_at,omitempty"`
	// TakenAt is when a drive acted on the answer, so no later run of it
	// acts again
	TakenAt time.Time `yaml:"taken_at,omitempty"`
}

// Ways in an ask was answered by.
const (
	ByPane      = "pane"      // typed in the agent's own pane
	ByAsks      = "asks"      // f10 asks answer, or its walk
	BySkill     = "skill"     // /f10:asks
	ByDrive     = "drive"     // f10 drive --answer
	ByCancelled = "cancelled" // its drive was cancelled
)

// A Question is one numbered question of an ask; Options[0] is the default.
type Question struct {
	N       int      `json:"n"                 yaml:"n"`
	Text    string   `json:"text"              yaml:"text"`
	Options []string `json:"options,omitempty" yaml:"options,omitempty"`
}

// Default is the answer the question takes when none is given.
func (q Question) Default() string {
	if len(q.Options) > 0 {
		return q.Options[0]
	}

	return ""
}

var numbered = regexp.MustCompile(`(?:^|\s)(\d+)\.\s`)

var options = regexp.MustCompile(`\[([^\[\]]*\|[^\[\]]*|[^\[\]|]+)\]\s*$`)

// Parse splits `1. Reuse the plan? [reuse | re-plan] 2. ...` into questions;
// text not numbered from 1 is one free-text question, so an odd ask still
// reaches the user whole.
func Parse(text string) []Question {
	text = strings.TrimSpace(text)

	locs := numbered.FindAllStringSubmatchIndex(text, -1)

	var qs []Question

	for i, loc := range locs {
		n, _ := strconv.Atoi(text[loc[2]:loc[3]])
		if n != len(qs)+1 {
			continue
		}

		end := len(text)
		for _, next := range locs[i+1:] {
			if m, _ := strconv.Atoi(text[next[2]:next[3]]); m == n+1 {
				end = next[0]

				break
			}
		}

		qs = append(qs, question(n, text[loc[1]:end]))
	}

	if len(qs) == 0 || locs[0][0] != 0 {
		return []Question{question(1, text)}
	}

	return qs
}

func question(n int, body string) Question {
	body = strings.TrimSpace(body)
	q := Question{N: n, Text: body}

	m := options.FindStringSubmatchIndex(body)
	if m == nil {
		return q
	}

	for o := range strings.SplitSeq(body[m[2]:m[3]], "|") {
		if o = strings.TrimSpace(o); o != "" {
			q.Options = append(q.Options, o)
		}
	}

	q.Text = strings.TrimSpace(body[:m[0]])

	return q
}

// Resolve maps value to an option by letter (`b`), number (`2`) or text;
// free text answers only a question without options, so a typo never picks
// a choice.
func Resolve(q Question, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("question %d needs an answer", q.N)
	}

	if len(q.Options) == 0 {
		return value, nil
	}

	if len(value) == 1 && value[0] >= 'a' && int(value[0]-'a') < len(q.Options) {
		return q.Options[value[0]-'a'], nil
	}

	if i, err := strconv.Atoi(value); err == nil && i >= 1 && i <= len(q.Options) {
		return q.Options[i-1], nil
	}

	for _, o := range q.Options {
		if strings.EqualFold(o, value) {
			return o, nil
		}
	}

	return "", fmt.Errorf("question %d takes %s, not %q", q.N, Letters(q.Options), value)
}

// Letters lists options as `a) reuse, b) re-plan`.
func Letters(opts []string) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		parts[i] = string(rune('a'+i)) + ") " + o
	}

	return strings.Join(parts, ", ")
}

// Format renders answers, by question number, as `1. re-plan 2. wait`,
// numbered as the ask was; a question with no answer takes its default.
func Format(qs []Question, answers map[int]string) string {
	parts := make([]string, 0, len(qs))

	for _, q := range qs {
		a := answers[q.N]
		if a == "" {
			a = q.Default()
		}

		parts = append(parts, strconv.Itoa(q.N)+". "+a)
	}

	return strings.Join(parts, " ")
}
