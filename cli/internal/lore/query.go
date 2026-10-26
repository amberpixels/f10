package lore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// How Why linked the blamed commit to a change.
const (
	ViaTrailer = "trailer"
	ViaAnchor  = "anchor"
)

// WhyResult is what lore knows about one line.
type WhyResult struct {
	Path    string   `json:"path"`
	Line    int      `json:"line"`
	Commit  string   `json:"commit"`
	Via     string   `json:"via"`
	Records []Record `json:"records"`
}

// Record is one change's part of a why answer.
type Record struct {
	Change  string  `json:"change"`
	Intent  *Entry  `json:"intent,omitempty"`
	Verdict *Entry  `json:"verdict,omitempty"`
	Entries []Entry `json:"entries"` // decisions, findings and lessons on the line
}

// Why answers for line of path, both repo-relative: blame the line, follow
// the commit's Change: trailers (else Task:) to their changes, and keep the
// entries anchored on the line. A commit with no trailer, or whose changes
// hold nothing, falls back to entries anchored at the blamed commit on the
// same path. Nothing found is ErrNoRecord.
func (s *Store) Why(ctx context.Context, path string, line int) (*WhyResult, error) {
	commit, err := s.Git.BlameLine(ctx, path, line)
	if err != nil {
		return nil, err
	}

	trailers, err := s.Git.Trailers(ctx, commit)
	if err != nil {
		return nil, err
	}

	res := &WhyResult{Path: path, Line: line, Commit: commit, Via: ViaTrailer}

	changes := trailers[TrailerChange]
	if len(changes) == 0 {
		changes = trailers[TrailerTask]
	}

	for _, change := range changes {
		if checkChangeID(change) != nil {
			continue
		}

		entries, err := s.Effective(change)
		if errors.Is(err, ErrNoRecord) {
			continue
		}

		if err != nil {
			return nil, err
		}

		res.Records = append(res.Records, recordFor(change, entries, onLine(entries, path, line)))
	}

	if len(res.Records) > 0 {
		return res, nil
	}

	res.Via = ViaAnchor

	all, err := s.Changes()
	if err != nil {
		return nil, err
	}

	for _, change := range all {
		entries, err := s.Effective(change)
		if err != nil {
			return nil, err
		}

		matched := filterEntries(entries, func(a Anchor) bool {
			return a.Path == path && sameCommit(a.Commit, commit)
		})
		if len(matched) > 0 {
			res.Records = append(res.Records, recordFor(change, entries, matched))
		}
	}

	if len(res.Records) == 0 {
		return nil, ErrNoRecord
	}

	return res, nil
}

// onLine keeps the anchored entries covering the line, and those sharing a
// symbol with an anchor that covers it, so a decision on a function still
// answers after the function grew.
func onLine(entries []Entry, path string, line int) []Entry {
	symbols := map[string]bool{}

	for _, e := range entries {
		for _, a := range e.Anchors {
			if a.Symbol != "" && a.Covers(path, line) {
				symbols[a.Symbol] = true
			}
		}
	}

	return filterEntries(entries, func(a Anchor) bool {
		return a.Covers(path, line) || (a.Path == path && a.Symbol != "" && symbols[a.Symbol])
	})
}

// filterEntries keeps the decisions, findings and live lessons with an
// anchor match accepts.
func filterEntries(entries []Entry, match func(Anchor) bool) []Entry {
	var out []Entry

	for _, e := range entries {
		switch e.Kind {
		case KindDecision, KindFinding:
		case KindLesson:
			if e.Retired {
				continue
			}
		case KindIntent, KindVerdict:
			continue
		}

		if slices.ContainsFunc(e.Anchors, match) {
			out = append(out, e)
		}
	}

	return out
}

//nolint:lostfield // picks the intent and latest verdict out of the change
func recordFor(change string, entries, matched []Entry) Record {
	r := Record{Change: change, Entries: matched}

	if r.Entries == nil {
		r.Entries = []Entry{}
	}

	for _, e := range entries {
		switch e.Kind {
		case KindIntent:
			if r.Intent == nil {
				r.Intent = &e
			}
		case KindVerdict:
			if r.Verdict == nil || later(e, *r.Verdict) {
				r.Verdict = &e
			}
		case KindDecision, KindFinding, KindLesson:
		}
	}

	return r
}

// sameCommit matches an abbreviated sha against a full one, either way.
func sameCommit(a, b string) bool {
	if len(a) < 7 || len(b) < 7 {
		return false
	}

	a, b = strings.ToLower(a), strings.ToLower(b)

	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// Lessons is every effective, non-retired lesson anchored to path, across
// every change, oldest first.
func (s *Store) Lessons(path string) ([]Entry, error) {
	changes, err := s.Changes()
	if err != nil {
		return nil, err
	}

	var out []Entry

	for _, change := range changes {
		entries, err := s.Effective(change)
		if err != nil {
			return nil, err
		}

		for _, e := range entries {
			if e.Kind != KindLesson || e.Retired {
				continue
			}

			if slices.ContainsFunc(e.Anchors, func(a Anchor) bool { return a.Path == path }) {
				out = append(out, e)
			}
		}
	}

	slices.SortStableFunc(out, func(a, b Entry) int {
		return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.ID, b.ID))
	})

	return out, nil
}

// CheckResult is the gate's answer: it passes with no reasons.
type CheckResult struct {
	Change  string   `json:"change"`
	Reasons []string `json:"reasons,omitempty"`
}

// Pass reports whether nothing held the change back.
func (c *CheckResult) Pass() bool { return len(c.Reasons) == 0 }

// Check fails a change with no intent, no verdict or a latest verdict that
// is not a proceed, or a blocker or major finding still open.
func (s *Store) Check(change string) (*CheckResult, error) {
	res := &CheckResult{Change: change}

	entries, err := s.Effective(change)
	if errors.Is(err, ErrNoRecord) {
		res.Reasons = []string{"no entries for " + change}

		return res, nil
	}

	if err != nil {
		return nil, err
	}

	rec := recordFor(change, entries, nil)

	if rec.Intent == nil {
		res.Reasons = append(res.Reasons, "no intent")
	}

	switch v := rec.Verdict; {
	case v == nil:
		res.Reasons = append(res.Reasons, "no verdict")
	case v.Outcome != "proceed" && v.Outcome != "proceed with changes":
		res.Reasons = append(res.Reasons, fmt.Sprintf("the latest verdict, %s, is %s (%s at %s)",
			v.ID, v.Outcome, v.Gate, v.Commit))
	}

	for _, e := range entries {
		if e.Kind == KindFinding && e.Status == "open" && (e.Severity == "blocker" || e.Severity == "major") {
			res.Reasons = append(res.Reasons, fmt.Sprintf("%s finding %s is open: %s",
				e.Severity, e.ID, firstLine(e.Body)))
		}
	}

	return res, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")

	return line
}
