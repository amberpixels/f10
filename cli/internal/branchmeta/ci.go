package branchmeta

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// The actions a triage records.
const (
	TriedFix   = "fix"
	TriedRerun = "rerun"
)

// A Triage is the CI key's value: `<sha>:<action>` entries, space-joined.
// A fix counts for the branch, since it moves the head; a rerun counts for
// its sha, since the head stays.
type Triage struct {
	Tried []Tried
}

// Tried is one recorded action on the head it was taken for.
type Tried struct {
	SHA, Action string
}

// ParseTriage reads the CI key's value, skipping malformed entries.
func ParseTriage(s string) Triage {
	var t Triage

	for f := range strings.FieldsSeq(s) {
		sha, action, ok := strings.Cut(f, ":")
		if ok && sha != "" && (action == TriedFix || action == TriedRerun) {
			t.Tried = append(t.Tried, Tried{SHA: sha, Action: action})
		}
	}

	return t
}

// Format is the value the CI key holds.
func (t Triage) Format() string {
	parts := make([]string, len(t.Tried))
	for i, e := range t.Tried {
		parts[i] = e.SHA + ":" + e.Action
	}

	return strings.Join(parts, " ")
}

// Fixed reports a fix round already spent in this red spell.
func (t Triage) Fixed() bool {
	return slices.ContainsFunc(t.Tried, func(e Tried) bool { return e.Action == TriedFix })
}

// Rerun reports a rerun already spent on sha.
func (t Triage) Rerun(sha string) bool {
	return slices.Contains(t.Tried, Tried{SHA: sha, Action: TriedRerun})
}

// Allows reports whether action may still be taken on sha.
func (t Triage) Allows(sha, action string) bool {
	if action == TriedFix {
		return !t.Fixed()
	}

	return !t.Rerun(sha)
}

// Lines is the record for a reader, one action a line.
func (t Triage) Lines() []string {
	out := make([]string, len(t.Tried))
	for i, e := range t.Tried {
		out[i] = e.Action + " at " + e.SHA[:min(7, len(e.SHA))]
	}

	return out
}

// RecordTriage adds action on sha to branch's record, refusing one the
// record no longer allows.
func RecordTriage(ctx context.Context, dir, branch, sha, action string) error {
	v, err := Get(ctx, dir, branch, CI)
	if err != nil {
		return err
	}

	t := ParseTriage(v)
	if !t.Allows(sha, action) {
		if action == TriedFix {
			return fmt.Errorf("%s: a fix round was already made since the checks were last green", branch)
		}

		return fmt.Errorf("%s: the failed jobs at %s were already rerun", branch, sha[:min(7, len(sha))])
	}

	t.Tried = append(t.Tried, Tried{SHA: sha, Action: action})

	return Set(ctx, dir, branch, CI, t.Format())
}

// ReadTriage is branch's record; absent is empty.
func ReadTriage(ctx context.Context, dir, branch string) (Triage, error) {
	v, err := Get(ctx, dir, branch, CI)

	return ParseTriage(v), err
}

// ClearTriage ends the red spell, once a head reads green.
func ClearTriage(ctx context.Context, dir, branch string) error {
	return UnsetIfSet(ctx, dir, branch, CI)
}

// ClearOnGreen ends the red spell when the head read green. Every reader
// calls it, since a fix counts for the branch and a record left past green
// would spend the next spell's fix round.
func ClearOnGreen(ctx context.Context, dir, branch string, green bool) error {
	if !green {
		return nil
	}

	return ClearTriage(ctx, dir, branch)
}
