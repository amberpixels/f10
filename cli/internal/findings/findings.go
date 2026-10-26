// Package findings reads a review's findings file, the shape steps/review.md
// fixes: a header of `key: value` lines, a `## Categories` list with a count
// per category, and a block per finding under `## Prior round` and
// `## Findings`, each a `### <id> <title>` heading with `key: value` lines,
// the `verdict:` and `reason:` that resolve appends among them.
package findings

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// File is one findings file.
type File struct {
	Task       string
	Round      string
	Reviewed   string // the sha, plus ` dirty` when the tree differed
	Reviewer   string
	Verdict    string // clean or findings
	Categories map[string]int
	Findings   []Finding
}

// Finding is one block. Prior-round blocks carry their round-one id and the
// status the second review gave them.
type Finding struct {
	ID       string
	Title    string
	Category string
	Prior    bool
	Status   string // prior round only: fixed, still open, ...
	Verdict  string // as written, attachments included: `skip +reply`
	Reason   string
}

var (
	heading     = regexp.MustCompile(`^###\s+(\d+\.\d+)\s+(.*)$`)
	priorStatus = regexp.MustCompile(
		`\s+-\s+(fixed|still open|fix introduced a problem|explanation accepted|explanation disputed)$`,
	)
	countLine = regexp.MustCompile(`^-\s+(.+?):\s+(.+)$`)
)

// ErrNoHeader is a file without the header's task and round.
var ErrNoHeader = errors.New("findings: no header")

// Parse reads a findings file's text.
func Parse(text string) (*File, error) {
	f := &File{Categories: map[string]int{}}
	section := "header"

	var cur *Finding

	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")

		if name, ok := strings.CutPrefix(line, "## "); ok {
			section = strings.ToLower(strings.TrimSpace(name))
			cur = nil

			continue
		}

		switch section {
		case "header":
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}

			value = strings.TrimSpace(value)

			switch strings.TrimSpace(key) {
			case "task":
				f.Task = value
			case "round":
				f.Round = value
			case "reviewed":
				f.Reviewed = value
			case "reviewer":
				f.Reviewer = value
			case "verdict":
				f.Verdict = value
			}
		case "categories":
			if m := countLine.FindStringSubmatch(line); m != nil {
				n, err := strconv.Atoi(strings.TrimSpace(m[2]))
				if err != nil {
					n = 0 // `none found`
				}

				f.Categories[m[1]] = n
			}
		case "prior round", "findings":
			if m := heading.FindStringSubmatch(line); m != nil {
				f.Findings = append(f.Findings, Finding{ID: m[1], Title: m[2], Prior: section == "prior round"})
				cur = &f.Findings[len(f.Findings)-1]

				if cur.Prior {
					if s := priorStatus.FindStringSubmatch(cur.Title); s != nil {
						cur.Status = s[1]
						cur.Title = strings.TrimSuffix(cur.Title, s[0])
					}
				}

				continue
			}

			if cur == nil {
				continue
			}

			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}

			value = strings.TrimSpace(value)

			switch key {
			case "category":
				cur.Category = value
			case "verdict":
				cur.Verdict = value
			case "reason":
				cur.Reason = value
			}
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("findings: %w", err)
	}

	if f.Task == "" || f.Round == "" {
		return f, ErrNoHeader
	}

	return f, nil
}

// Reopens reports whether a prior finding's status awaits a verdict.
func Reopens(status string) bool {
	switch status {
	case "still open", "fix introduced a problem", "explanation disputed":
		return true
	default:
		return false
	}
}
