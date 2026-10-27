// Package lore keeps a change's intent, decisions, findings, verdicts and
// lessons as immutable markdown entries next to the code, and answers why a
// line is the way it is and what to know before editing a file.
// docs/lore.md is the spec.
//
// The package imports nothing else from this module, so it can be copied
// out as a standalone tool: git goes through the Git interface, and the
// root is always a parameter.
package lore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"go.yaml.in/yaml/v3"
)

// Kind is an entry's kind, in the order a change lives through them.
type Kind string

const (
	KindIntent   Kind = "intent"
	KindDecision Kind = "decision"
	KindFinding  Kind = "finding"
	KindVerdict  Kind = "verdict"
	KindLesson   Kind = "lesson"
)

// The vocabularies the enumerated fields accept.
var (
	Kinds      = []Kind{KindIntent, KindDecision, KindFinding, KindVerdict, KindLesson}
	Severities = []string{"blocker", "major", "minor", "nit"}
	Statuses   = []string{"open", "fixed", "wontfix", "duplicate"}
	// Outcomes are a judge's four verdicts, spelled as its verdict files spell them.
	Outcomes = []string{"proceed", "proceed with changes", "rethink", "stop"}
	Gates    = []string{"judge", "review", "human"}
	// Sources are where a finding came from: a local review or the host's.
	Sources = []string{"local", "remote"}
	// Judged is what a verdict judged: the plan, or the change itself.
	Judged = []string{"plan", "change"}
)

// Agent is who did the work when it was not the human alone.
type Agent struct {
	Model   string `json:"model,omitempty"   yaml:"model,omitempty"`
	Via     string `json:"via,omitempty"     yaml:"via,omitempty"`
	Session string `json:"session,omitempty" yaml:"session,omitempty"`
}

// Anchor is where an entry points in the code. EndLine 0 means the anchor
// covers Line alone.
type Anchor struct {
	Path    string `json:"path"               yaml:"path"`
	Line    int    `json:"line"               yaml:"line"`
	EndLine int    `json:"end_line,omitempty" yaml:"end_line,omitempty"`
	Commit  string `json:"commit"             yaml:"commit"`
	Symbol  string `json:"symbol,omitempty"   yaml:"symbol,omitempty"`
}

// Covers reports whether the anchor is on path and its lines include line.
func (a Anchor) Covers(file string, line int) bool {
	end := max(a.EndLine, a.Line)

	return a.Path == file && line >= a.Line && line <= end
}

// String is the anchor in the form `--anchor` takes.
func (a Anchor) String() string {
	s := a.Path + ":" + strconv.Itoa(a.Line)
	if a.EndLine > a.Line {
		s += "-" + strconv.Itoa(a.EndLine)
	}

	if a.Commit != "" {
		s += "@" + a.Commit
	}

	if a.Symbol != "" {
		s += "#" + a.Symbol
	}

	return s
}

// Rejected is an option a decision did not take, and why.
type Rejected struct {
	Option string `json:"option" yaml:"option"`
	Reason string `json:"reason" yaml:"reason"`
}

// Entry is one lore file. ID comes from the filename, never the frontmatter;
// the per-kind fields are flat, and Validate rejects one on another kind.
type Entry struct {
	ID         string    `json:"id"                   yaml:"-"`
	Kind       Kind      `json:"kind"                 yaml:"kind"`
	Change     string    `json:"change"               yaml:"change"`
	Human      string    `json:"human"                yaml:"human"`
	Agent      *Agent    `json:"agent,omitempty"      yaml:"agent,omitempty"`
	At         time.Time `json:"at"                   yaml:"at"`
	Anchors    []Anchor  `json:"anchors,omitempty"    yaml:"anchors,omitempty"`
	Supersedes string    `json:"supersedes,omitempty" yaml:"supersedes,omitempty"`

	Chosen   string     `json:"chosen,omitempty"   yaml:"chosen,omitempty"`
	Rejected []Rejected `json:"rejected,omitempty" yaml:"rejected,omitempty"`

	Severity   string `json:"severity,omitempty"   yaml:"severity,omitempty"`
	Status     string `json:"status,omitempty"     yaml:"status,omitempty"`
	Resolution string `json:"resolution,omitempty" yaml:"resolution,omitempty"`
	Source     string `json:"source,omitempty"     yaml:"source,omitempty"`

	Outcome string `json:"outcome,omitempty" yaml:"outcome,omitempty"`
	Gate    string `json:"gate,omitempty"    yaml:"gate,omitempty"`
	Commit  string `json:"commit,omitempty"  yaml:"commit,omitempty"`
	Judged  string `json:"judged,omitempty"  yaml:"judged,omitempty"`

	Retired bool `json:"retired,omitempty" yaml:"retired,omitempty"`

	Body string `json:"body" yaml:"-"`

	file string // the base name Parse read it from
}

// FileName is the entry's name inside its change directory.
func (e *Entry) FileName() string {
	return e.ID + "-" + string(e.Kind) + ".md"
}

// ErrBadFileName is a file in a change directory not named `<ulid>-<kind>.md`.
var ErrBadFileName = errors.New("lore: not an entry file name")

// splitFileName reads `<ulid>-<kind>.md`.
func splitFileName(name string) (string, Kind, error) {
	stem, ok := strings.CutSuffix(name, ".md")
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrBadFileName, name)
	}

	id, kind, ok := strings.Cut(stem, "-")
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrBadFileName, name)
	}

	if _, err := ulid.ParseStrict(id); err != nil {
		return "", "", fmt.Errorf("%w: %s: %w", ErrBadFileName, name, err)
	}

	return id, Kind(kind), nil
}

// Parse reads one entry file. name is the file's base name, which carries
// the id; a kind that disagrees with the frontmatter is Validate's to report.
func Parse(name string, data []byte) (Entry, error) {
	id, _, err := splitFileName(name)
	if err != nil {
		return Entry{}, err
	}

	block, body, ok := splitFrontmatter(data)
	if !ok {
		return Entry{}, fmt.Errorf("lore: %s: no frontmatter", name)
	}

	var e Entry

	dec := yaml.NewDecoder(bytes.NewReader(block))
	dec.KnownFields(true)

	if err := dec.Decode(&e); err != nil && !errors.Is(err, io.EOF) {
		return Entry{}, fmt.Errorf("lore: %s: %w", name, err)
	}

	e.ID = id
	e.file = name
	e.Body = strings.TrimSpace(string(body))

	return e, nil
}

// Marshal renders an entry as its file: frontmatter, a blank line, the body.
func Marshal(e Entry) ([]byte, error) {
	e.At = e.At.UTC()

	head, err := yaml.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("lore: %w", err)
	}

	var b bytes.Buffer

	b.WriteString("---\n")
	b.Write(head)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(e.Body))
	b.WriteString("\n")

	return b.Bytes(), nil
}

var anchorRe = regexp.MustCompile(`^(.+?):(\d+)(?:-(\d+))?(?:@([0-9a-fA-F]+))?(?:#(.+))?$`)

// ParseAnchor reads `path:line[-end][@commit][#symbol]`. The commit may be
// left out; Validate requires one by the time the entry is written.
func ParseAnchor(s string) (Anchor, error) {
	m := anchorRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Anchor{}, fmt.Errorf("lore: anchor %q is not path:line[-end][@commit][#symbol]", s)
	}

	a := Anchor{Path: path.Clean(m[1]), Commit: m[4], Symbol: m[5]}
	a.Line, _ = strconv.Atoi(m[2])

	if m[3] != "" {
		a.EndLine, _ = strconv.Atoi(m[3])
	}

	if a.Line < 1 || (a.EndLine != 0 && a.EndLine < a.Line) {
		return Anchor{}, fmt.Errorf("lore: anchor %q: lines run from 1 and end_line is not before line", s)
	}

	return a, nil
}

// splitFrontmatter returns the YAML between a leading `---` line and the
// next one, and the body after it.
func splitFrontmatter(data []byte) ([]byte, []byte, bool) {
	const fence = "---"

	first, rest, found := bytes.Cut(data, []byte("\n"))
	if !found || strings.TrimRight(string(first), " \t\r") != fence {
		return nil, data, false
	}

	var block []byte

	for len(rest) > 0 {
		line, after, _ := bytes.Cut(rest, []byte("\n"))
		if strings.TrimRight(string(line), " \t\r") == fence {
			return block, after, true
		}

		block = append(append(block, line...), '\n')
		rest = after
	}

	return nil, data, false
}
