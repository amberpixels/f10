// Package verdict reads a judge's verdict file, the shape steps/judge.md
// fixes: a header of `key: value` lines up to the first `## ` heading, then
// prose the parser ignores.
package verdict

import (
	"bufio"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Verdicts are the four values a `verdict:` line may hold.
var Verdicts = []string{"proceed", "proceed with changes", "rethink", "stop"}

// File is one verdict file's header.
type File struct {
	Task     string // empty for an idea
	Subject  string // kind and reference: `task FT-76`, `idea <one line>`, ...
	Verdict  string
	Reason   string
	Run      string // standalone, ship before pr, ship after pr
	Blind    bool
	Decision string // ship runs only
}

// Idea reports whether the subject is an idea, which belongs to no task.
func (f *File) Idea() bool {
	kind, _, _ := strings.Cut(f.Subject, " ")

	return kind == "idea"
}

var (
	// ErrNoVerdict is a header without a `verdict:` line.
	ErrNoVerdict = errors.New("verdict: no verdict line")
	// ErrBadVerdict is a `verdict:` value outside Verdicts.
	ErrBadVerdict = errors.New("verdict: unknown verdict")
)

// Parse reads a verdict file's text.
func Parse(text string) (*File, error) {
	f := &File{}

	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		if strings.HasPrefix(line, "## ") {
			break
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		value = strings.TrimSpace(value)

		switch strings.TrimSpace(key) {
		case "task":
			f.Task = value
		case "subject":
			f.Subject = value
		case "verdict":
			f.Verdict = value
		case "reason":
			f.Reason = value
		case "run":
			f.Run = value
		case "blind":
			f.Blind = value == "yes"
		case "decision":
			f.Decision = value
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("verdict: %w", err)
	}

	switch {
	case f.Verdict == "":
		return f, ErrNoVerdict
	case !slices.Contains(Verdicts, f.Verdict):
		return f, fmt.Errorf("%w: %q", ErrBadVerdict, f.Verdict)
	}

	return f, nil
}
