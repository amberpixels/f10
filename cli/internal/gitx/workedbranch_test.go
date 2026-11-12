package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
)

// A task's checkout is a linked worktree, where the rebase state sits under
// the main git dir's worktrees/, and each rebase backend keeps its own.
func TestWorkedBranchDuringStoppedRebase(t *testing.T) {
	t.Parallel()

	r := newRealRepo(t)
	r.git("checkout", "-q", "side")
	r.commit("f", "side\n", "side")
	r.git("checkout", "-q", "main")

	wt := filepath.Join(t.TempDir(), "task")
	r.git("worktree", "add", "-q", "-b", "FT-1/task", wt, "main")

	if err := os.WriteFile(filepath.Join(wt, "f"), []byte("task\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	gitxtest.Run(t, wt, "commit", "-q", "-am", "task")

	for _, backend := range []string{"--merge", "--apply"} {
		stop := exec.Command("git", "rebase", backend, "side")
		stop.Dir = wt

		if out, err := stop.CombinedOutput(); err == nil {
			t.Fatalf("rebase %s did not conflict:\n%s", backend, out)
		}

		if plain, err := Branch(t.Context(), wt); err != nil || plain != "" {
			t.Fatalf("rebase %s: HEAD still names %q (%v), so nothing is under test", backend, plain, err)
		}

		if got, err := WorkedBranch(t.Context(), wt); err != nil || got != "FT-1/task" {
			t.Errorf("rebase %s: branch %q, err %v", backend, got, err)
		}

		gitxtest.Run(t, wt, "rebase", "--abort")
	}

	if got, err := WorkedBranch(t.Context(), wt); err != nil || got != "FT-1/task" {
		t.Errorf("checked out: branch %q, err %v", got, err)
	}

	gitxtest.Run(t, wt, "checkout", "-q", "--detach")

	if got, err := WorkedBranch(t.Context(), wt); err != nil || got != "" {
		t.Errorf("detached, no rebase: branch %q, err %v", got, err)
	}
}
