package layout

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The catchup step is read as conflict resolution when a merge or rebase is
// already in progress, so the loader states that above the step, before any
// rule; a tree at rest, and every other step, print nothing.
func TestCatchupInProgressLine(t *testing.T) {
	script := scriptPath(t)
	repo := mkRepo(t)
	home := t.TempDir()

	gitc := func(args ...string) {
		t.Helper()
		run(t, repo, append([]string{
			"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
		}, args...)...)
	}

	file := filepath.Join(repo, "a.txt")
	writeFile(t, file, "base\n")
	gitc("add", "a.txt")
	gitc("commit", "-q", "-m", "base")
	gitc("checkout", "-q", "-b", "other")
	writeFile(t, file, "theirs\n")
	gitc("commit", "-q", "-am", "theirs")
	gitc("checkout", "-q", "-")
	writeFile(t, file, "ours\n")
	gitc("commit", "-q", "-am", "ours")

	short := gitOut(t, repo, "rev-parse", "--short", "other")
	want := func(op string) string {
		return "in progress: " + op + " other (" + short + "), 1 unmerged file"
	}

	t.Run("at rest", func(t *testing.T) {
		if out := loadOut(t, script, repo, home, "catchup"); hasLine(out, "in progress: ") {
			t.Fatalf("a tree at rest printed an in-progress line:\n%s", out)
		}
	})

	t.Run("merge", func(t *testing.T) {
		mustFail(t, repo, "merge", "other")
		defer gitc("merge", "--abort")

		out := loadOut(t, script, repo, home, "catchup")
		assertLineUnderStep(t, out, want("merge with"))

		if other := loadOut(t, script, repo, home, "plan"); hasLine(other, "in progress: ") {
			t.Fatalf("a step other than catchup printed the line:\n%s", other)
		}
	})

	t.Run("rebase", func(t *testing.T) {
		mustFail(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "rebase", "other")
		defer gitc("rebase", "--abort")

		assertLineUnderStep(t, loadOut(t, script, repo, home, "catchup"), want("rebase onto"))
	})
}

// hasLine says whether any whole line of out opens with prefix: the step's
// own prose quotes the line's shape, so a substring match would always hit.
func hasLine(out, prefix string) bool {
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}

	return false
}

// assertLineUnderStep wants the line right under the catchup step header,
// where it is the first thing a reader of the step sees.
func assertLineUnderStep(t *testing.T, out, want string) {
	t.Helper()

	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if l != "--- step: catchup (generic) ---" {
			continue
		}

		if i+1 < len(lines) && lines[i+1] == want {
			return
		}

		t.Fatalf("under the catchup header: got %q, want %q", lines[i+1], want)
	}

	t.Fatalf("no catchup step header in:\n%s", out)
}

// loadOut runs the loader for steps in dir with HOME pointed away from the
// developer's own ~/.f10 and returns its whole output.
func loadOut(t *testing.T, script, dir, home string, steps ...string) string {
	t.Helper()

	cmd := exec.Command("bash", append([]string{script}, steps...)...)
	cmd.Dir = dir

	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}

	cmd.Env = append(cmd.Env, "HOME="+home)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("f10-load-project.sh failed: %v\n%s", err, out)
	}

	return string(out)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}

	return strings.TrimSpace(string(out))
}

// mustFail runs a git command that is expected to stop with a non-zero exit,
// the way a conflicting merge or rebase does.
func mustFail(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("git %s succeeded, expected a conflict:\n%s", strings.Join(args, " "), out)
	}
}
