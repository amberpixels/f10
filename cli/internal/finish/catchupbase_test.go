package finish

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Releasing a dependent records its base tip before the base branch goes, so
// its catchup can drop the squashed commits.
func TestReleaseDependentsRecordsTip(t *testing.T) {
	r, tip := testkit.NewStackedRepo(t)
	r.At(testkit.Later, "config", "--unset", branchmeta.Tip.For("FT-1/a"))

	if _, err := ReleaseDependents(t.Context(), r.Dir, "FT-2/b", "main", true); err != nil {
		t.Fatal(err)
	}

	if got := r.At(testkit.Later, "config", branchmeta.Tip.For("FT-1/a")); got != tip {
		t.Errorf("tip %q, want %s", got, tip)
	}
}

// A spike closed without merging never reached main: its dependent keeps no
// tip, so its catchup keeps the spike's code.
func TestReleaseDependentsSpikeClearsTip(t *testing.T) {
	r, _ := testkit.NewStackedRepo(t)

	if _, err := ReleaseDependents(t.Context(), r.Dir, "FT-2/b", "main", false); err != nil {
		t.Fatal(err)
	}

	if got, err := gitx.Config(t.Context(), r.Dir, branchmeta.Tip.For("FT-1/a")); err != nil || got != "" {
		t.Errorf("tip %q (%v) after a spike closed, want none", got, err)
	}

	if p := r.Resolve(r.Input("")); p.Method == stack.MethodOnto {
		t.Errorf("method %q after a spike closed, want today's", p.Method)
	}
}
