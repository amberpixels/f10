package statusview

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/amberpixels/f10/cli/internal/testkit"
)

// The edge fixture's off-vocabulary verdicts (`asked`, `ask+reply`) count as
// found and as nothing else.
func TestReviewRoundsOnTheEdgeFixture(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{"1.md", "2.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "findings", "testdata", "edge", name))
		if err != nil {
			t.Fatal(err)
		}

		testkit.WriteFile(t, filepath.Join(root, "reviews", "FT-1", name), string(raw))
	}

	o := fileOutcomes(root, "FT-1", time.Time{})

	want := []reviewRound{
		{Round: "1", Found: 6, Fixed: 1, Skipped: 1, Ask: 3},
		{Round: "2", Found: 4, Fixed: 1, Skipped: 1, Ask: 1},
	}
	if !slices.Equal(o.Local, want) || o.LocalRaised != 7 {
		t.Errorf("local = %+v raised %d, want %+v raised 7", o.Local, o.LocalRaised, want)
	}
}
