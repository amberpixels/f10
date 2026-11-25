package notation_test

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/notation"
)

// Format then Parse returns the same entries, for one entry and for a chain,
// over every part an entry has.
func TestFormatThenParse(t *testing.T) {
	t.Parallel()

	tasks := []string{"A-1", "12", "ft-3", "A-1-x", "GH-1_v2", "A-1+A-2", "B-7+B-8+B-9"}
	bases := []string{"C-1", "D-2", "E-3+E-4", "44"}
	profiles := []string{"", "opus", "claude:opus:high"}

	rng := rand.New(rand.NewPCG(1, 2))

	entry := func() notation.Entry {
		e := notation.Entry{Task: tasks[rng.IntN(len(tasks))], Profile: profiles[rng.IntN(len(profiles))]}

		for _, b := range bases {
			if rng.IntN(3) == 0 {
				e.Bases = append(e.Bases, b)
			}
		}

		return e
	}

	for range 500 {
		one := entry()

		got, err := notation.Parse(notation.Format(one))
		if err != nil || !reflect.DeepEqual(got, []notation.Entry{one}) {
			t.Fatalf("Parse(Format(%+v)) = %+v, %v", one, got, err)
		}

		// a chain: each entry after the first is stacked on the one before it
		chain := []notation.Entry{entry()}

		for range rng.IntN(4) {
			next := entry()
			next.Bases = append([]string{chain[len(chain)-1].ID()}, next.Bases...)
			chain = append(chain, next)
		}

		tok := notation.FormatChain(chain)

		got, err = notation.Parse(tok)
		if err != nil || !reflect.DeepEqual(got, chain) {
			t.Fatalf("Parse(%q) = %+v, %v, want %+v", tok, got, err, chain)
		}
	}
}

func TestEntryParts(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		task, id, suffix, members string
	}{
		{"A-1", "A-1", "", "A-1"},
		{"A-1-x", "A-1", "x", "A-1"},
		{"GH-1_v2", "GH-1", "v2", "GH-1"},
		{"A-1+A-2", "A-1+A-2", "", "A-1 A-2"},
		{"https://h/o/r/issues/2", "https://h/o/r/issues/2", "", "https://h/o/r/issues/2"},
	} {
		e := notation.Entry{Task: c.task}
		if e.ID() != c.id || e.Suffix() != c.suffix || strings.Join(e.Members(), " ") != c.members {
			t.Errorf("%s: id %q suffix %q members %v", c.task, e.ID(), e.Suffix(), e.Members())
		}
	}
}

// A url is taken whole with the marks its path or query holds. Only an
// order typed around it is refused, and the refusal names the fix.
func TestURL(t *testing.T) {
	t.Parallel()

	for _, tok := range []string{
		"https://github.com/o/r/issues/2",
		"https://h/o/r/pull/2?a=1,2&u=x@y",
		"https://user@h/o/r/pull/2",
		"https://h/a,b/pull/7",
	} {
		got, err := notation.Parse(tok)
		if err != nil || len(got) != 1 || got[0].Task != tok {
			t.Errorf("Parse(%q) = %+v, %v", tok, got, err)
		}
	}

	for _, tok := range []string{
		"https://github.com/o/r/issues/2,FT-623",
		"https://github.com/o/r/issues/2@FT-623",
		"https://github.com/o/r/issues/2,FT-623:opus",
		"FT-1,https://github.com/o/r/issues/2",
		"FT-1@https://github.com/o/r/issues/2",
	} {
		if _, err := notation.Parse(tok); err == nil || !strings.Contains(err.Error(), "use the task's id") {
			t.Errorf("Parse(%q) err = %v, want the refusal naming the id", tok, err)
		}
	}
}

func TestIsID(t *testing.T) {
	t.Parallel()

	for word, want := range map[string]bool{
		"A-1": true, "12": true, "A-1-x": true, "A-1+A-2": true,
		"": false, "-x": false, "A-1:opus": false, "word": false, "A-1,A-2": false,
	} {
		if got := notation.IsID(word); got != want {
			t.Errorf("IsID(%q) = %v, want %v", word, got, want)
		}
	}
}
