package lore

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func ids(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}

	return out
}

func TestAddWritesOneFileAndFillsHuman(t *testing.T) {
	r := newRepo(t)
	s := testStore(t, filepath.Join(r.dir, ".lore"), CLIGit{Dir: r.dir})

	e := intent("FT-1", "2026-10-20T09:00:00Z")
	e.Human = ""

	id := s.mustAdd(t, e)

	data, err := os.ReadFile(filepath.Join(s.Root, "FT-1", id+"-intent.md"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "human: dev@example.com\n") {
		t.Errorf("human not taken from git:\n%s", data)
	}

	if _, err := s.Add(t.Context(), Entry{ID: id, Kind: KindIntent}); err == nil {
		t.Error("an entry with an id was written again")
	}
}

func TestAddWithoutEmail(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	dir := t.TempDir()
	newRepoAt(t, dir)

	s := testStore(t, filepath.Join(dir, ".lore"), CLIGit{Dir: dir})
	e := intent("FT-1", "2026-10-20T09:00:00Z")
	e.Human = ""

	if _, err := s.Add(t.Context(), e); err == nil || !strings.Contains(err.Error(), "user.email") {
		t.Errorf("err = %v, want one naming user.email", err)
	}
}

// newRepoAt inits a repository with no identity at all.
func newRepoAt(t *testing.T, dir string) {
	t.Helper()

	r := gitRepo{t: t, dir: dir}
	r.git("init", "-q")
}

func TestAddRefusesBadSupersedes(t *testing.T) {
	s := testStore(t, t.TempDir(), nil)

	first := s.mustAdd(t, finding("FT-1", "2026-10-20T09:00:00Z", "major", "open"))
	other := s.mustAdd(t, intent("FT-2", "2026-10-20T09:00:00Z"))

	fixed := finding("FT-1", "2026-10-20T10:00:00Z", "major", "fixed")
	fixed.Supersedes = first
	second := s.mustAdd(t, fixed)

	cases := []struct {
		name string
		e    Entry
		want string
	}{
		{
			"missing target",
			withSupersedes(finding("FT-1", "2026-10-20T11:00:00Z", "major", "fixed"), "01M5C8KYM0248H248H248H248H"),
			"no such entry",
		},
		{
			"other change",
			withSupersedes(finding("FT-1", "2026-10-20T11:00:00Z", "major", "fixed"), other),
			"no such entry",
		},
		{
			"other kind",
			withSupersedes(lesson("FT-1", "2026-10-20T11:00:00Z", anchorAt("a.go", 1, 0, "abcdef1", "")), first),
			"cannot supersede",
		},
		{
			"fork",
			withSupersedes(finding("FT-1", "2026-10-20T11:00:00Z", "major", "wontfix"), first),
			"supersede the chain's head, " + second,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Add(t.Context(), c.e)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func withSupersedes(e Entry, id string) Entry {
	e.Supersedes = id

	return e
}

// write puts an entry file in place by hand, skipping Add's checks, the
// way a merge of two branches can.
func write(t *testing.T, s *Store, e Entry) {
	t.Helper()

	data, err := Marshal(e)
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(s.Root, e.Change)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, e.FileName()), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEffectiveChains(t *testing.T) {
	const (
		idA = "01M5C8KYM0248H248H248H248A"
		idB = "01M5C8KYM0248H248H248H248B"
		idC = "01M5C8KYM0248H248H248H248C"
		idD = "01M5C8KYM0248H248H248H248D"
		idI = "01M5C8KYM0248H248H248H2480"
		idX = "01M5C8KYM0248H248H248H248X"
	)

	entry := func(id string, e Entry, supersedes string) Entry {
		e.ID, e.Supersedes = id, supersedes

		return e
	}

	in := intent("FT-1", "2026-10-20T12:00:00Z")
	f := func(when, status string) Entry { return finding("FT-1", when, "major", status) }

	cases := []struct {
		name    string
		entries []Entry
		want    []string
	}{
		{
			"three-step chain leaves its last",
			[]Entry{
				entry(idA, f("2026-10-20T09:00:00Z", "open"), ""),
				entry(idB, f("2026-10-20T10:00:00Z", "wontfix"), idA),
				entry(idC, f("2026-10-20T11:00:00Z", "fixed"), idB),
				entry(idI, in, ""),
			},
			[]string{idI, idC},
		},
		{
			"fork keeps the later head",
			[]Entry{
				entry(idA, f("2026-10-20T09:00:00Z", "open"), ""),
				entry(idB, f("2026-10-20T11:00:00Z", "fixed"), idA),
				entry(idC, f("2026-10-20T10:00:00Z", "wontfix"), idA),
			},
			[]string{idB},
		},
		{
			"dangling supersedes stays effective",
			[]Entry{
				entry(idA, f("2026-10-20T09:00:00Z", "open"), ""),
				entry(idD, f("2026-10-20T10:00:00Z", "fixed"), idX),
			},
			[]string{idA, idD},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testStore(t, t.TempDir(), nil)
			for _, e := range c.entries {
				write(t, s, e)
			}

			got, err := s.Effective("FT-1")
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(ids(got), c.want) {
				t.Errorf("effective = %v, want %v", ids(got), c.want)
			}
		})
	}
}

func TestStoreValidate(t *testing.T) {
	s := testStore(t, t.TempDir(), nil)

	a := finding("FT-1", "2026-10-20T09:00:00Z", "major", "open")
	a.ID = "01M5C8KYM0248H248H248H248A"
	b := withSupersedes(finding("FT-1", "2026-10-20T10:00:00Z", "major", "fixed"), a.ID)
	b.ID = "01M5C8KYM0248H248H248H248B"
	c := withSupersedes(finding("FT-1", "2026-10-20T11:00:00Z", "major", "wontfix"), a.ID)
	c.ID = "01M5C8KYM0248H248H248H248C"
	dangling := withSupersedes(intent("FT-1", "2026-10-20T12:00:00Z"), "01M5C8KYM0248H248H248H248X")
	dangling.ID = "01M5C8KYM0248H248H248H248D"
	cross := withSupersedes(lesson("FT-1", "2026-10-20T13:00:00Z", anchorAt("a.go", 1, 0, "abcdef1", "")), a.ID)
	cross.ID = "01M5C8KYM0248H248H248H248E"
	elsewhere := intent("FT-2", "2026-10-20T09:00:00Z")
	elsewhere.ID = "01M5C8KYM0248H248H248H248F"

	for _, e := range []Entry{a, b, c, dangling, cross} {
		write(t, s, e)
	}

	// an entry filed under the wrong change, and one whose name lies about its kind
	misplaced := filepath.Join(s.Root, "FT-1", elsewhere.FileName())
	data, _ := Marshal(elsewhere)
	_ = os.WriteFile(misplaced, data, 0o644)

	data, _ = Marshal(verdictEntry("FT-1", "2026-10-20T14:00:00Z", "proceed"))
	_ = os.WriteFile(filepath.Join(s.Root, "FT-1", "01M5C8KYM0248H248H248H248G-lesson.md"), data, 0o644)
	_ = os.WriteFile(
		filepath.Join(s.Root, "FT-1", "01M5C8KYM0248H248H248H248H-intent.md"),
		[]byte("no frontmatter\n"),
		0o644,
	)

	problems, err := s.Validate("FT-1")
	if err != nil {
		t.Fatal(err)
	}

	var text []string
	for _, p := range problems {
		text = append(text, p.String())
	}

	all := strings.Join(text, "\n")

	for _, want := range []string{
		a.ID + " is superseded by " + b.ID + " and " + c.ID + ": the chain forks",
		"supersedes 01M5C8KYM0248H248H248H248X, which is not in FT-1",
		"a lesson supersedes " + a.ID + ", a finding",
		`change "FT-2" is not its directory, "FT-1"`,
		`kind "verdict" does not match the file name`,
		"01M5C8KYM0248H248H248H248H-intent.md: no frontmatter",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no problem %q in:\n%s", want, all)
		}
	}

	if _, err := s.Validate("FT-9"); !errors.Is(err, ErrNoRecord) {
		t.Errorf("missing change: err = %v, want ErrNoRecord", err)
	}
}

func TestLoadMissingChange(t *testing.T) {
	s := testStore(t, t.TempDir(), nil)

	if _, err := s.Load("FT-1"); !errors.Is(err, ErrNoRecord) {
		t.Errorf("err = %v, want ErrNoRecord", err)
	}

	if changes, err := s.Changes(); err != nil || changes != nil {
		t.Errorf("changes = %v, %v; want none", changes, err)
	}
}

// Two branches add to one change in a committed root; the merge has no
// conflict and keeps both.
func TestBranchesMergeWithoutConflict(t *testing.T) {
	r := newRepo(t)
	s := testStore(t, filepath.Join(r.dir, ".lore"), CLIGit{Dir: r.dir})

	s.mustAdd(t, intent("FT-1", "2026-10-20T09:00:00Z"))
	r.commit("intent", nil)

	r.git("switch", "-q", "-c", "a")
	s.mustAdd(t, finding("FT-1", "2026-10-20T10:00:00Z", "minor", "open"))
	r.commit("finding on a", nil)

	r.git("switch", "-q", "main")
	r.git("switch", "-q", "-c", "b")
	s.mustAdd(t, finding("FT-1", "2026-10-20T10:00:00Z", "minor", "open"))
	r.commit("finding on b", nil)

	r.git("switch", "-q", "a")
	r.git("merge", "-q", "--no-edit", "b")

	entries, err := s.Load("FT-1")
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 3 {
		t.Errorf("after the merge: %d entries, want 3", len(entries))
	}

	if out := r.git("ls-files", ".lore"); strings.Count(out, "\n")+1 != 3 {
		t.Errorf("tracked lore files:\n%s", out)
	}
}
