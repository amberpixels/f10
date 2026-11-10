package findings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The edge fixture holds every verdict shape the readers disagree on: the
// capitalized `Ask`, the off-vocabulary `asked` and `ask+reply`, and prior
// blocks with and without a verdict.
func TestRoundsOnTheEdgeFixture(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("testdata", "edge")

	st, err := Rounds(dir, []string{filepath.Join(dir, "1.md")})
	if err != nil {
		t.Fatal(err)
	}

	if want := []Open{{ID: "1.6", Title: "No verdict", Why: "no verdict"}}; st.Decline != Unsettled ||
		!reflect.DeepEqual(st.Open, want) {
		t.Errorf("round one = %s %+v, want unsettled %+v", st.Decline, st.Open, want)
	}

	st, err = Rounds(dir, []string{filepath.Join(dir, "1.md"), filepath.Join(dir, "2.md")})
	if err != nil {
		t.Fatal(err)
	}

	if want := []Open{{ID: "1.3", Title: "Asked one", Why: "still open"}}; st.Decline != Exhausted ||
		!reflect.DeepEqual(st.Open, want) {
		t.Errorf("round two = %s %+v, want exhausted %+v", st.Decline, st.Open, want)
	}
}

func TestTally(t *testing.T) {
	t.Parallel()

	read := func(name string) *File {
		raw, err := os.ReadFile(filepath.Join("testdata", "edge", name))
		if err != nil {
			t.Fatal(err)
		}

		f, err := Parse(string(raw))
		if err != nil {
			t.Fatal(err)
		}

		return f
	}

	if got, want := read("1.md").Tally(), (Tally{Found: 6, Fixed: 1, Skipped: 1, Open: 3, Raised: 6}); got != want {
		t.Errorf("round one = %+v, want %+v", got, want)
	}

	// 1.6's `fixed` prior block is not counted; the reopened ones are
	if got, want := read("2.md").Tally(), (Tally{Found: 4, Fixed: 1, Skipped: 1, Open: 1, Raised: 1}); got != want {
		t.Errorf("round two = %+v, want %+v", got, want)
	}

	both, err := Parse("task: FT-1\nround: 1\nreviewer: local, claude[bot]\nverdict: findings\n\n## Findings\n\n" +
		"### 1.1 A\nsource: local\nverdict: fix\n\n### 1.2 B\nsource: remote\nverdict: ask\n\n### 1.3 C\n")
	if err != nil {
		t.Fatal(err)
	}

	if got := both.TallyHalf(true); got != (Tally{Found: 1, Open: 1, Raised: 1}) {
		t.Errorf("remote half = %+v", got)
	}

	// an untagged block in a file both halves wrote is the local half's
	if got := both.TallyHalf(false); got != (Tally{Found: 2, Fixed: 1, Open: 1, Raised: 2}) {
		t.Errorf("local half = %+v", got)
	}
}
