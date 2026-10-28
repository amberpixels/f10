package lore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line\n")
	}

	return b.String()
}

// whyFixture is a repository whose commits carry each kind of link, and a
// lore root describing them.
type whyFixture struct {
	repo                        gitRepo
	store                       *Store
	limit, alias, bundle, plain string // the commits
}

func newWhyFixture(t *testing.T) whyFixture {
	t.Helper()

	r := newRepo(t)
	fx := whyFixture{repo: r, store: testStore(t, filepath.Join(r.dir, ".lore"), CLIGit{Dir: r.dir})}

	fx.limit = r.commit("Add the limiter\n\nChange: FT-1\nTask: FT-7", map[string]string{"api/limit.go": lines(40)})
	fx.alias = r.commit("Alias\n\nTask: FT-2", map[string]string{"alias.go": lines(5)})
	fx.bundle = r.commit("Bundle\n\nTask: FT-3\nTask: FT-4", map[string]string{"bundle.go": lines(5)})
	fx.plain = r.commit("No trailer", map[string]string{"plain.go": lines(5), "other.go": lines(5)})

	s := fx.store
	s.mustAdd(t, intent("FT-1", "2026-10-20T09:00:00Z"))
	s.mustAdd(t, verdictEntry("FT-1", "2026-10-20T10:00:00Z", "rethink"))
	s.mustAdd(t, verdictEntry("FT-1", "2026-10-21T10:00:00Z", "proceed"))

	d := Entry{
		Kind: KindDecision, Change: "FT-1", Human: "dev@example.com", At: at("2026-10-20T09:30:00Z"),
		Agent:   &Agent{Model: "claude-opus-5-5", Via: "claude-code"},
		Anchors: []Anchor{anchorAt("api/limit.go", 10, 20, fx.limit[:7], "Allow")},
		Chosen:  "token bucket", Rejected: []Rejected{{Option: "fixed window", Reason: "bursts at the edge"}},
		Body: "A bucket per key.\n\nIn memory: one instance.",
	}
	s.mustAdd(t, d)

	// on the symbol, not the line
	s.mustAdd(
		t,
		finding(
			"FT-1",
			"2026-10-20T11:00:00Z",
			"major",
			"fixed",
			anchorAt("api/limit.go", 30, 0, fx.limit[:7], "Allow"),
		),
	)
	// on the file, neither line nor symbol
	off := d
	off.At, off.Anchors, off.Agent = at(
		"2026-10-20T11:30:00Z",
	), []Anchor{
		anchorAt("api/limit.go", 35, 0, fx.limit[:7], ""),
	}, nil
	s.mustAdd(t, off)

	retired := lesson("FT-1", "2026-10-20T12:00:00Z", anchorAt("api/limit.go", 15, 0, fx.limit[:7], ""))
	id := s.mustAdd(t, retired)
	retired.Supersedes, retired.Retired, retired.At = id, true, at("2026-10-20T13:00:00Z")
	s.mustAdd(t, retired)

	s.mustAdd(t, lesson("FT-2", "2026-10-20T09:00:00Z", anchorAt("alias.go", 2, 0, fx.alias[:7], "")))
	s.mustAdd(t, intent("FT-7", "2026-10-20T09:00:00Z"))
	s.mustAdd(t, intent("FT-3", "2026-10-20T09:00:00Z"))
	s.mustAdd(t, intent("FT-4", "2026-10-20T09:00:00Z"))
	s.mustAdd(t, intent("FT-5", "2026-10-20T09:00:00Z"))
	s.mustAdd(t, lesson("FT-5", "2026-10-20T10:00:00Z", anchorAt("plain.go", 4, 0, fx.plain, "")))
	// a commit this clone lacks, as after a squash: never an ancestor
	s.mustAdd(t, lesson("FT-6", "2026-10-20T10:00:00Z", anchorAt("plain.go", 4, 0, "1234567", "")))

	return fx
}

func TestWhy(t *testing.T) {
	fx := newWhyFixture(t)

	// a line not committed yet
	if err := os.WriteFile(filepath.Join(fx.repo.dir, "other.go"), []byte(lines(6)), 0o644); err != nil {
		t.Fatal(err)
	}

	type want struct {
		via     string
		changes []string
		entries []Kind // of the first record
		noRec   bool
		err     bool
	}

	cases := []struct {
		name string
		path string
		line int
		want want
	}{
		{
			"Change: trailer wins over Task:",
			"api/limit.go",
			15,
			want{via: ViaTrailer, changes: []string{"FT-1"}, entries: []Kind{KindDecision, KindFinding}},
		},
		{"off the anchors", "api/limit.go", 2, want{via: ViaTrailer, changes: []string{"FT-1"}, entries: []Kind{}}},
		{"Task: alias", "alias.go", 2, want{via: ViaTrailer, changes: []string{"FT-2"}, entries: []Kind{KindLesson}}},
		{
			"two Task: lines",
			"bundle.go",
			1,
			want{via: ViaTrailer, changes: []string{"FT-3", "FT-4"}, entries: []Kind{}},
		},
		{
			"no trailer, anchored at the commit",
			"plain.go",
			1,
			want{via: ViaAnchor, changes: []string{"FT-5"}, entries: []Kind{KindLesson}},
		},
		{"no trailer, nothing anchored", "other.go", 2, want{noRec: true}},
		{"not committed yet", "other.go", 6, want{noRec: true}},
		{"unknown path", "nope.go", 1, want{err: true}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := fx.store.Why(t.Context(), c.path, c.line, "")

			switch {
			case c.want.noRec:
				if !errors.Is(err, ErrNoRecord) {
					t.Fatalf("err = %v, want ErrNoRecord", err)
				}

				return
			case c.want.err:
				if err == nil || errors.Is(err, ErrNoRecord) {
					t.Fatalf("err = %v, want a git error", err)
				}

				return
			case err != nil:
				t.Fatal(err)
			}

			var changes []string
			for _, r := range res.Records {
				changes = append(changes, r.Change)
			}

			if res.Via != c.want.via || !slices.Equal(changes, c.want.changes) {
				t.Errorf("via %s %v, want via %s %v", res.Via, changes, c.want.via, c.want.changes)
			}

			kinds := []Kind{}
			for _, e := range res.Records[0].Entries {
				kinds = append(kinds, e.Kind)
			}

			if !slices.Equal(kinds, c.want.entries) {
				t.Errorf("entries %v, want %v", kinds, c.want.entries)
			}
		})
	}
}

func TestWhyRecordHasIntentAndLatestVerdict(t *testing.T) {
	fx := newWhyFixture(t)

	res, err := fx.store.Why(t.Context(), "api/limit.go", 15, "")
	if err != nil {
		t.Fatal(err)
	}

	rec := res.Records[0]
	if rec.Intent == nil || rec.Verdict == nil || rec.Verdict.Outcome != "proceed" {
		t.Errorf("intent %v, verdict %+v; want the intent and the proceed verdict", rec.Intent, rec.Verdict)
	}
}

func TestLessonsSkipRetiredAndOtherPaths(t *testing.T) {
	fx := newWhyFixture(t)

	cases := map[string]int{"api/limit.go": 0, "alias.go": 1, "plain.go": 2, "missing.go": 0}
	for path, want := range cases {
		got, err := fx.store.Lessons(path)
		if err != nil {
			t.Fatal(err)
		}

		if len(got) != want {
			t.Errorf("%s: %d lessons, want %d", path, len(got), want)
		}
	}
}

func TestCheck(t *testing.T) {
	s := testStore(t, t.TempDir(), nil)
	anchor := anchorAt("a.go", 1, 0, "abcdef1", "")

	cases := []struct {
		name    string
		entries []Entry
		want    []string // reason substrings; none means it passes
	}{
		{"no entries", nil, []string{"no entries"}},
		{"no intent", []Entry{verdictEntry("C", "2026-10-20T10:00:00Z", "proceed")}, []string{"no intent"}},
		{"no verdict", []Entry{intent("C", "2026-10-20T09:00:00Z")}, []string{"no verdict"}},
		{
			"rethink",
			[]Entry{intent("C", "2026-10-20T09:00:00Z"), verdictEntry("C", "2026-10-20T10:00:00Z", "rethink")},
			[]string{"is rethink"},
		},
		{
			"stop",
			[]Entry{intent("C", "2026-10-20T09:00:00Z"), verdictEntry("C", "2026-10-20T10:00:00Z", "stop")},
			[]string{"is stop"},
		},
		{
			"proceed",
			[]Entry{intent("C", "2026-10-20T09:00:00Z"), verdictEntry("C", "2026-10-20T10:00:00Z", "proceed")},
			nil,
		},
		{
			"proceed with changes",
			[]Entry{
				intent("C", "2026-10-20T09:00:00Z"),
				verdictEntry("C", "2026-10-20T10:00:00Z", "proceed with changes"),
			},
			nil,
		},
		{"a later stop wins", []Entry{
			intent("C", "2026-10-20T09:00:00Z"),
			verdictEntry("C", "2026-10-20T10:00:00Z", "proceed"),
			verdictEntry("C", "2026-10-20T11:00:00Z", "stop"),
		}, []string{"is stop"}},
		{"open blocker", []Entry{
			intent("C", "2026-10-20T09:00:00Z"), verdictEntry("C", "2026-10-20T10:00:00Z", "proceed"),
			finding("C", "2026-10-20T11:00:00Z", "blocker", "open", anchor),
		}, []string{"blocker finding"}},
		{"open major", []Entry{
			intent("C", "2026-10-20T09:00:00Z"), verdictEntry("C", "2026-10-20T10:00:00Z", "proceed"),
			finding("C", "2026-10-20T11:00:00Z", "major", "open"),
		}, []string{"major finding"}},
		{"open minor passes", []Entry{
			intent("C", "2026-10-20T09:00:00Z"), verdictEntry("C", "2026-10-20T10:00:00Z", "proceed"),
			finding("C", "2026-10-20T11:00:00Z", "minor", "open"),
		}, nil},
		{
			"every reason at once",
			[]Entry{finding("C", "2026-10-20T11:00:00Z", "major", "open")},
			[]string{"no intent", "no verdict", "major finding"},
		},
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			change := "C-" + string(rune('a'+i))

			for _, e := range c.entries {
				e.Change = change
				s.mustAdd(t, e)
			}

			res, err := s.Check(change)
			if err != nil {
				t.Fatal(err)
			}

			if len(res.Reasons) != len(c.want) {
				t.Fatalf("reasons %q, want %d like %q", res.Reasons, len(c.want), c.want)
			}

			for j, w := range c.want {
				if !strings.Contains(res.Reasons[j], w) {
					t.Errorf("reason %q lacks %q", res.Reasons[j], w)
				}
			}
		})
	}
}

// A settled finding no longer holds the gate.
func TestCheckFindingSettledBySupersedes(t *testing.T) {
	s := testStore(t, t.TempDir(), nil)

	s.mustAdd(t, intent("C", "2026-10-20T09:00:00Z"))
	s.mustAdd(t, verdictEntry("C", "2026-10-20T10:00:00Z", "proceed"))
	open := s.mustAdd(t, finding("C", "2026-10-20T11:00:00Z", "blocker", "open"))

	if res, _ := s.Check("C"); res.Pass() {
		t.Fatal("passes with an open blocker")
	}

	s.mustAdd(t, withSupersedes(finding("C", "2026-10-20T12:00:00Z", "blocker", "fixed"), open))

	if res, _ := s.Check("C"); !res.Pass() {
		t.Errorf("fails after the fix: %q", res.Reasons)
	}
}

func TestGolden(t *testing.T) {
	fx := newWhyFixture(t)

	render := map[string]func(*bytes.Buffer) error{
		"show": func(b *bytes.Buffer) error {
			entries, err := fx.store.Effective("FT-1")
			if err != nil {
				return err
			}

			return RenderShow(b, "FT-1", entries)
		},
		"why-trailer": func(b *bytes.Buffer) error {
			res, err := fx.store.Why(t.Context(), "api/limit.go", 15, "")
			if err != nil {
				return err
			}

			return RenderWhy(b, res)
		},
		"why-anchor": func(b *bytes.Buffer) error {
			res, err := fx.store.Why(t.Context(), "plain.go", 4, "")
			if err != nil {
				return err
			}

			return RenderWhy(b, res)
		},
		"lessons": func(b *bytes.Buffer) error {
			lessons, err := fx.store.Lessons("plain.go")
			if err != nil {
				return err
			}

			return RenderLessons(b, "plain.go", lessons)
		},
	}

	for name, fn := range render {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			if err := fn(&b); err != nil {
				t.Fatal(err)
			}

			golden := filepath.Join("testdata", "golden", name+".txt")

			if os.Getenv("F10_UPDATE") != "" {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(golden, b.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (F10_UPDATE=1 writes it)", err)
			}

			if !bytes.Equal(b.Bytes(), want) {
				t.Errorf("differs from %s:\n%s", golden, b.String())
			}
		})
	}
}

// The order a change is actually written in: the decision first, anchored
// at the HEAD before the code, then the code in a commit with no trailer.
func TestWhyFallbackFindsEntryWrittenBeforeItsCode(t *testing.T) {
	r := newRepo(t)
	s := testStore(t, filepath.Join(r.dir, ".lore"), CLIGit{Dir: r.dir})

	base := r.commit("base", map[string]string{"x.go": lines(5)})

	d := Entry{
		Kind: KindDecision, Change: "FT-1", Human: "dev@example.com", At: at("2026-10-20T09:00:00Z"),
		Anchors: []Anchor{anchorAt("x.go", 3, 0, base[:7], "")}, Chosen: "b", Body: "why b",
	}
	s.mustAdd(t, d)

	elsewhere := d
	elsewhere.Anchors = []Anchor{anchorAt("x.go", 5, 0, base[:7], "")}
	s.mustAdd(t, elsewhere)

	r.commit("change line 3", map[string]string{"x.go": "line\nline\nchanged\nline\nline\n"})

	res, err := s.Why(t.Context(), "x.go", 3, "")
	if err != nil {
		t.Fatal(err)
	}

	if res.Via != ViaAnchor || len(res.Records) != 1 || len(res.Records[0].Entries) != 1 {
		t.Fatalf("why = %+v, want the one decision on line 3 via its anchor", res)
	}

	// line 5 was not touched: blame names base itself, the exact match
	res, err = s.Why(t.Context(), "x.go", 5, "")
	if err != nil || len(res.Records[0].Entries) != 2 {
		t.Errorf("why x.go:5 = %+v, %v; want both decisions anchored at base", res, err)
	}
}

// A stray file in one change never fails a query about another.
func TestQueriesSkipStrayFiles(t *testing.T) {
	fx := newWhyFixture(t)

	for name, data := range map[string]string{
		"FT-9/README.md": "notes\n",
		"FT-9/01M5C8KYM0248H248H248H248H-lesson.md": "---\nkind: [broken\n---\n",
	} {
		path := filepath.Join(fx.store.Root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got, err := fx.store.Lessons("plain.go"); err != nil || len(got) != 2 {
		t.Errorf("lessons = %d, %v; want 2", len(got), err)
	}

	if _, err := fx.store.Why(t.Context(), "plain.go", 1, ""); err != nil {
		t.Errorf("why: %v", err)
	}

	if problems, err := fx.store.Validate("FT-9"); err != nil || len(problems) != 2 {
		t.Errorf("validate FT-9 = %v, %v; want both files reported", problems, err)
	}
}

// A reviewer asks about a line as the base had it: the answer is the change
// that wrote it, not the one rewriting it in the working tree.
func TestWhyAtARevision(t *testing.T) {
	fx := newWhyFixture(t)

	fx.store.mustAdd(t, intent("FT-8", "2026-10-21T09:00:00Z"))
	fx.repo.commit(
		"Rewrite the alias\n\nTask: FT-8",
		map[string]string{"alias.go": "line\nchanged\nline\nline\nline\n"},
	)

	now, err := fx.store.Why(t.Context(), "alias.go", 2, "")
	if err != nil || len(now.Records) != 1 || now.Records[0].Change != "FT-8" || now.Rev != "" {
		t.Fatalf("why at the working tree = %+v, %v; want FT-8", now, err)
	}

	then, err := fx.store.Why(t.Context(), "alias.go", 2, fx.alias)
	if err != nil || len(then.Records) != 1 || then.Records[0].Change != "FT-2" || then.Rev != fx.alias {
		t.Fatalf("why at %s = %+v, %v; want FT-2", fx.alias[:7], then, err)
	}

	// plain.go did not exist yet at that revision
	if _, err := fx.store.Why(t.Context(), "plain.go", 1, fx.alias); !errors.Is(err, ErrNoRecord) {
		t.Errorf("why for a path absent at the revision = %v, want ErrNoRecord", err)
	}
}
