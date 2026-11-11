package stack

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
)

// The fork point and the recorded base tip are one merge base: when the
// local and origin copies of the base diverged, both take origin's.
func TestNewerMergeBaseTakesOriginOnDivergedCopies(t *testing.T) {
	t.Parallel()

	r := gitxtest.Init(t, filepath.Join(t.TempDir(), "repo"), "main")
	r.Commit("2026-10-10T10:00:00Z", "a", "a\n", "c0")
	r.Git("checkout", "-q", "-b", "o")
	r.Commit("2026-10-10T11:00:00Z", "o", "o\n", "origin's")
	origin := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	r.Git("checkout", "-q", "main")
	r.Commit("2026-10-10T12:00:00Z", "l", "l\n", "local's")
	local := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	r.Git("update-ref", "refs/remotes/origin/main", origin)
	r.Git("checkout", "-q", "-b", "feat")
	r.At("2026-10-10T13:00:00Z", "merge", "-q", "--no-edit", "o")

	got, err := NewerMergeBase(t.Context(), r.Dir, "feat", "main")
	if err != nil {
		t.Fatal(err)
	}

	if got != origin {
		t.Errorf("merge base = %s, want origin's %s (local's is %s)", got, origin, local)
	}

	if fp, err := ForkPoint(t.Context(), r.Dir, "feat", "main"); err != nil || fp != got {
		t.Errorf("ForkPoint = %s, %v; want %s", fp, err, got)
	}
}
