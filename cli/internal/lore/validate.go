package lore

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/oklog/ulid/v2"
)

// ValidationError lists every problem found, not the first.
type ValidationError struct {
	Problems []string
}

func (v *ValidationError) Error() string {
	return "lore: invalid entry: " + strings.Join(v.Problems, "; ")
}

// Validate checks one entry against the schema in docs/lore.md, on its own:
// what needs the rest of the change (supersedes targets, forks) is
// Store.Validate's. It returns nil or a *ValidationError.
func Validate(e Entry) error {
	var bad []string

	add := func(format string, args ...any) { bad = append(bad, fmt.Sprintf(format, args...)) }

	if e.ID != "" {
		if _, err := ulid.ParseStrict(e.ID); err != nil {
			add("id %q is not a ULID", e.ID)
		}
	}

	if !slices.Contains(Kinds, e.Kind) {
		add("kind %q is not one of %s", e.Kind, joinKinds())
	}

	if err := checkChangeID(e.Change); err != nil {
		add("%s", err)
	}

	if strings.TrimSpace(e.Human) == "" {
		add("human is required")
	}

	if e.At.IsZero() {
		add("at is required")
	}

	if strings.TrimSpace(e.Body) == "" {
		add("the body is empty")
	}

	if e.Supersedes != "" {
		if _, err := ulid.ParseStrict(e.Supersedes); err != nil {
			add("supersedes %q is not a ULID", e.Supersedes)
		}
	}

	for i, a := range e.Anchors {
		for _, p := range anchorProblems(a) {
			add("anchor %d: %s", i+1, p)
		}
	}

	kindFields(e, add)

	if bad == nil {
		return nil
	}

	return &ValidationError{Problems: bad}
}

// kindFields checks the per-kind fields: required ones on their kind,
// none of them on another.
func kindFields(e Entry, add func(string, ...any)) {
	own := func(kind Kind, field string, set bool) {
		if set && e.Kind != kind {
			add("%s is only for a %s", field, kind)
		}
	}

	own(KindDecision, "chosen", e.Chosen != "")
	own(KindDecision, "rejected", e.Rejected != nil)
	own(KindFinding, "severity", e.Severity != "")
	own(KindFinding, "status", e.Status != "")
	own(KindFinding, "resolution", e.Resolution != "")
	own(KindVerdict, "outcome", e.Outcome != "")
	own(KindVerdict, "gate", e.Gate != "")
	own(KindVerdict, "commit", e.Commit != "")
	own(KindLesson, "retired", e.Retired)

	switch e.Kind {
	case KindDecision:
		if strings.TrimSpace(e.Chosen) == "" {
			add("chosen is required")
		}

		for i, r := range e.Rejected {
			if strings.TrimSpace(r.Option) == "" || strings.TrimSpace(r.Reason) == "" {
				add("rejected %d: option and reason are required", i+1)
			}
		}
	case KindFinding:
		oneOf(add, "severity", e.Severity, Severities)
		oneOf(add, "status", e.Status, Statuses)

		if e.Status != "open" && e.Status != "" && strings.TrimSpace(e.Resolution) == "" {
			add("resolution is required unless the status is open")
		}
	case KindVerdict:
		oneOf(add, "outcome", e.Outcome, Outcomes)
		oneOf(add, "gate", e.Gate, Gates)

		if !isSHA(e.Commit) {
			add("commit %q is not a sha", e.Commit)
		}
	case KindLesson:
		if len(e.Anchors) == 0 {
			add("a lesson needs at least one anchor")
		}

		if e.Retired && e.Supersedes == "" {
			add("retired is only allowed on an entry that supersedes another")
		}
	case KindIntent:
	}
}

func oneOf(add func(string, ...any), field, value string, allowed []string) {
	if !slices.Contains(allowed, value) {
		add("%s %q is not one of %s", field, value, strings.Join(allowed, ", "))
	}
}

func anchorProblems(a Anchor) []string {
	var bad []string

	switch {
	case a.Path == "":
		bad = append(bad, "path is required")
	case path.IsAbs(a.Path) || path.Clean(a.Path) != a.Path || strings.HasPrefix(a.Path, "../") || a.Path == "..":
		bad = append(bad, fmt.Sprintf("path %q is not a clean repo-relative path", a.Path))
	}

	if a.Line < 1 {
		bad = append(bad, "line runs from 1")
	}

	if a.EndLine != 0 && a.EndLine < a.Line {
		bad = append(bad, "end_line is before line")
	}

	if !isSHA(a.Commit) {
		bad = append(bad, fmt.Sprintf("commit %q is not a sha", a.Commit))
	}

	return bad
}

// checkChangeID refuses an id that cannot be one directory name.
func checkChangeID(id string) error {
	if id == "" || id == "." || id == ".." || strings.HasPrefix(id, ".") ||
		strings.ContainsAny(id, `/\`) || strings.TrimSpace(id) != id {
		return fmt.Errorf("change %q is not a usable change id", id)
	}

	return nil
}

// isSHA accepts an abbreviated or full hex object name.
func isSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}

	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}

	return true
}

func joinKinds() string {
	names := make([]string, len(Kinds))
	for i, k := range Kinds {
		names[i] = string(k)
	}

	return strings.Join(names, ", ")
}
