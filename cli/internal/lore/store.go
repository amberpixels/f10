package lore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Store is one lore root: a directory per change, a file per entry.
type Store struct {
	Root string
	Git  Git
	// Now and Entropy default to the clock and ulid's monotonic source; a
	// test sets them for stable ids.
	Now     func() time.Time
	Entropy io.Reader
}

// Add validates e against the schema and the rest of its change, writes it,
// and returns its id. Human comes from git when unset, At from the clock.
// There is no update or delete: a file is created once and never rewritten.
func (s *Store) Add(ctx context.Context, e Entry) (string, error) {
	if e.ID != "" {
		return "", errors.New("lore: a new entry has no id yet")
	}

	if e.Human == "" && s.Git != nil {
		email, err := s.Git.UserEmail(ctx)

		switch {
		case errors.Is(err, ErrNoRecord):
			return "", errors.New("lore: no human: set git config user.email")
		case err != nil:
			return "", err
		}

		e.Human = email
	}

	if e.At.IsZero() {
		e.At = s.now()
	}

	// the id keeps the milliseconds `at` drops, so entries written in one
	// second by separate runs still sort in the order they were written
	id, err := ulid.New(ulid.Timestamp(e.At), s.entropy())
	e.At = e.At.UTC().Truncate(time.Second)
	if err != nil {
		return "", fmt.Errorf("lore: %w", err)
	}

	e.ID = id.String()

	if e.Supersedes != "" {
		s.inherit(&e)
	}

	if err := Validate(e); err != nil {
		return "", err
	}

	if e.Supersedes != "" {
		if err := s.checkSupersedes(e); err != nil {
			return "", err
		}
	}

	data, err := Marshal(e)
	if err != nil {
		return "", err
	}

	dir := filepath.Join(s.Root, e.Change)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // committed with the code, read by every clone
		return "", fmt.Errorf("lore: %w", err)
	}

	//nolint:gosec // committed with the code, read by every clone
	f, err := os.OpenFile(filepath.Join(dir, e.FileName()), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("lore: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()

		return "", fmt.Errorf("lore: %w", err)
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("lore: %w", err)
	}

	return e.ID, nil
}

// headOf follows supersedes forward from id to the entry nothing supersedes.
func headOf(entries []Entry, id string) string {
	seen := map[string]bool{}

	for !seen[id] {
		seen[id] = true

		i := slices.IndexFunc(entries, func(x Entry) bool { return x.Supersedes == id })
		if i < 0 {
			break
		}

		id = entries[i].ID
	}

	return id
}

// Load reads every entry of the change, by id, which is time order.
// A change with no directory is ErrNoRecord; a file that does not parse
// is an error naming it.
func (s *Store) Load(change string) ([]Entry, error) {
	entries, problems, err := s.load(change)
	if err != nil {
		return nil, err
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("lore: %s", problems[0])
	}

	return entries, nil
}

// Problem is one thing Validate found, in the file it names.
type Problem struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.File + ": " + p.Message }

// Effective is the change's entries with every superseded one dropped,
// the intent first, then by time. A fork keeps its later head.
func (s *Store) Effective(change string) ([]Entry, error) {
	entries, err := s.Load(change)
	if err != nil {
		return nil, err
	}

	return effective(entries), nil
}

func effective(entries []Entry) []Entry {
	byID := make(map[string]Entry, len(entries))
	superseded := map[string]bool{}

	for _, e := range entries {
		byID[e.ID] = e

		if e.Supersedes != "" {
			superseded[e.Supersedes] = true
		}
	}

	// a head per chain root; a fork leaves two heads on one root
	best := map[string]Entry{}

	var roots []string

	for _, e := range entries {
		if superseded[e.ID] {
			continue
		}

		root := rootOf(byID, e)

		cur, ok := best[root]
		if !ok {
			roots = append(roots, root)
		}

		if !ok || later(e, cur) {
			best[root] = e
		}
	}

	out := make([]Entry, 0, len(roots))
	for _, r := range roots {
		out = append(out, best[r])
	}

	slices.SortStableFunc(out, func(a, b Entry) int {
		if ai, bi := a.Kind == KindIntent, b.Kind == KindIntent; ai != bi {
			if ai {
				return -1
			}

			return 1
		}

		if c := a.At.Compare(b.At); c != 0 {
			return c
		}

		return cmp.Compare(a.ID, b.ID)
	})

	return out
}

// rootOf follows supersedes back to the first entry of the chain; a
// dangling target ends the walk at the entry that names it.
func rootOf(byID map[string]Entry, e Entry) string {
	seen := map[string]bool{}

	for e.Supersedes != "" && !seen[e.ID] {
		seen[e.ID] = true

		prev, ok := byID[e.Supersedes]
		if !ok {
			break
		}

		e = prev
	}

	return e.ID
}

func later(a, b Entry) bool {
	if c := a.At.Compare(b.At); c != 0 {
		return c > 0
	}

	return a.ID > b.ID
}

// Changes lists the change directories under the root, sorted. No root is
// no changes.
func (s *Store) Changes() ([]string, error) {
	dirs, err := os.ReadDir(s.Root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("lore: %w", err)
	}

	var out []string

	for _, d := range dirs {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			out = append(out, d.Name())
		}
	}

	return out, nil
}

// Validate lints every entry of the change: the schema, the filename and
// directory against the frontmatter, and the supersedes rules.
func (s *Store) Validate(change string) ([]Problem, error) {
	entries, problems, err := s.load(change)
	if err != nil {
		return nil, err
	}

	byID := map[string]Entry{}
	for _, e := range entries {
		byID[e.ID] = e
	}

	supersededBy := map[string][]string{}

	for _, e := range entries {
		name := e.file
		if name == "" {
			name = e.FileName()
		}

		file := filepath.Join(change, name)
		bad := func(format string, args ...any) {
			problems = append(problems, Problem{File: file, Message: fmt.Sprintf(format, args...)})
		}

		var ve *ValidationError
		if err := Validate(e); errors.As(err, &ve) {
			for _, p := range ve.Problems {
				bad("%s", p)
			}
		}

		if e.Change != change {
			bad("change %q is not its directory, %q", e.Change, change)
		}

		if _, kind, _ := splitFileName(name); kind != e.Kind {
			bad("kind %q does not match the file name", e.Kind)
		}

		if e.Supersedes == "" {
			continue
		}

		target, ok := byID[e.Supersedes]

		switch {
		case !ok:
			bad("supersedes %s, which is not in %s", e.Supersedes, change)
		case target.Kind != e.Kind:
			bad("a %s supersedes %s, a %s", e.Kind, e.Supersedes, target.Kind)
		default:
			supersededBy[e.Supersedes] = append(supersededBy[e.Supersedes], e.ID)
		}
	}

	for target, ids := range supersededBy {
		if len(ids) > 1 {
			problems = append(problems, Problem{
				File:    change,
				Message: fmt.Sprintf("%s is superseded by %s: the chain forks", target, strings.Join(ids, " and ")),
			})
		}
	}

	slices.SortStableFunc(problems, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Message, b.Message))
	})

	return problems, nil
}

// effectiveLoose is Effective for a query across changes: a file that does
// not parse is skipped, so one stray file cannot fail every path's answer.
// validate is where it gets reported.
func (s *Store) effectiveLoose(change string) ([]Entry, error) {
	entries, _, err := s.load(change)
	if err != nil {
		return nil, err
	}

	return effective(entries), nil
}

// checkSupersedes refuses a target outside the change, of another kind, or
// already superseded, which would fork the chain.
func (s *Store) checkSupersedes(e Entry) error {
	entries, err := s.Load(e.Change)
	if err != nil && !errors.Is(err, ErrNoRecord) {
		return err
	}

	i := slices.IndexFunc(entries, func(x Entry) bool { return x.ID == e.Supersedes })
	if i < 0 {
		return fmt.Errorf("lore: supersedes %s: no such entry in %s", e.Supersedes, e.Change)
	}

	if entries[i].Kind != e.Kind {
		return fmt.Errorf("lore: supersedes %s: a %s cannot supersede a %s", e.Supersedes, e.Kind, entries[i].Kind)
	}

	for _, x := range entries {
		if x.Supersedes == e.Supersedes {
			head := headOf(entries, x.ID)

			return fmt.Errorf("lore: %s is already superseded by %s; supersede the chain's head, %s",
				e.Supersedes, x.ID, head)
		}
	}

	return nil
}

// inherit fills a superseding finding's severity, source and anchors from
// the entry it supersedes where it leaves them out, so settling a finding
// needs only its new status. A missing target is checkSupersedes' to report.
func (s *Store) inherit(e *Entry) {
	if e.Kind != KindFinding {
		return
	}

	entries, err := s.Load(e.Change)
	if err != nil {
		return
	}

	i := slices.IndexFunc(entries, func(x Entry) bool { return x.ID == e.Supersedes })
	if i < 0 || entries[i].Kind != e.Kind {
		return
	}

	old := entries[i]
	e.Severity = cmp.Or(e.Severity, old.Severity)
	e.Source = cmp.Or(e.Source, old.Source)

	if len(e.Anchors) == 0 {
		e.Anchors = old.Anchors
	}
}

// load reads a change, returning a parse failure as a problem, not an error.
func (s *Store) load(change string) ([]Entry, []Problem, error) {
	if err := checkChangeID(change); err != nil {
		return nil, nil, fmt.Errorf("lore: %w", err)
	}

	dir := filepath.Join(s.Root, change)

	files, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, ErrNoRecord
	}

	if err != nil {
		return nil, nil, fmt.Errorf("lore: %w", err)
	}

	var (
		entries  []Entry
		problems []Problem
	)

	for _, f := range files {
		if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
			continue
		}

		rel := filepath.Join(change, f.Name())

		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("lore: %w", err)
		}

		e, err := Parse(f.Name(), data)
		if err != nil {
			msg := strings.TrimPrefix(strings.TrimPrefix(err.Error(), "lore: "), f.Name()+": ")
			problems = append(problems, Problem{File: rel, Message: msg})

			continue
		}

		entries = append(entries, e)
	}

	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.ID, b.ID) })

	return entries, problems, nil
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}

	return time.Now()
}

func (s *Store) entropy() io.Reader {
	if s.Entropy != nil {
		return s.Entropy
	}

	return ulid.DefaultEntropy()
}
