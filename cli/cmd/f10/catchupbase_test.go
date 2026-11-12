package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestCatchupBaseCommand(t *testing.T) {
	r := testkit.NewCatchupRepo(t)

	dir := filepath.Join(r.Dir, ".f10", "instructions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	md := "---\ncatchup: merge\n---\n# demo\n"
	if err := os.WriteFile(filepath.Join(dir, "project.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, error) {
		return runApp(t, append([]string{"-C", r.Dir, "pr", "catchup", "--no-fetch"}, args...)...).outErr()
	}

	for _, guard := range [][]string{nil, {"--guard", "judge"}} {
		out, err := run(guard...)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range []string{"base: origin/main\n", "method: merge\n", "why: catchup: merge\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: no %q in\n%s", guard, want, out)
			}
		}
	}

	if _, err := run("--guard", "bogus"); err == nil || !strings.Contains(err.Error(), "want implement, pr or judge") {
		t.Errorf("--guard bogus: %v", err)
	}
}

// A stopped rebase detaches HEAD, so the mark has to find the branch being
// rebased; written under an empty branch name, git refuses the whole config.
func TestCatchupMarkDuringStoppedOperation(t *testing.T) {
	for _, method := range []string{"rebase", "merge"} {
		t.Run(method, func(t *testing.T) {
			r, _ := testkit.NewStackedRepo(t)

			r.At(testkit.Later, "checkout", "-q", "FT-2/b")
			r.Commit(testkit.Later, "a.go", "package main // a, from the base\n", "base touches a")
			r.At(testkit.Later, "checkout", "-q", "FT-1/a")

			sha := r.Sha("FT-2/b")

			stop := exec.Command("git", method, "FT-2/b")
			stop.Dir = r.Dir

			if out, err := stop.CombinedOutput(); err == nil {
				t.Fatalf("git %s did not conflict:\n%s", method, out)
			}

			mark := string(branchmeta.Moved) + " " + sha

			// padded, as a pasted row may be: what is stored and printed is not
			padded := " " + string(branchmeta.Moved) + "   " + sha

			out, err := runApp(t, "-C", r.Dir, "pr", "catchup", "--mark", padded).outErr()
			if err != nil {
				t.Fatalf("--mark: %v\n%s", err, out)
			}

			if want := "branch: FT-1/a\nmark: " + mark + "\n"; out != want {
				t.Errorf("output %q, want %q", out, want)
			}

			if got := r.At(testkit.Later, "config", "--get", branchmeta.Moved.For("FT-1/a")); got != sha {
				t.Errorf("mark on FT-1/a: %q, want %s", got, sha)
			}

			cfg, err := os.ReadFile(filepath.Join(r.Dir, ".git", "config"))
			if err != nil {
				t.Fatal(err)
			}

			if strings.Contains(string(cfg), `[branch ""]`) {
				t.Errorf("a key under an empty branch name:\n%s", cfg)
			}

			r.At(testkit.Later, "status", "--short") // fatal on a config git refuses
		})
	}
}

func TestCatchupMarkRefusesWhatIsNoMark(t *testing.T) {
	r, _ := testkit.NewStackedRepo(t)

	for _, mark := range []string{"", "f10-task FT-9", "f10-base-moved", "f10-landed  ", "f10-base-moved not-a-commit"} {
		_, err := runApp(t, "-C", r.Dir, "pr", "catchup", "--mark="+mark).outErr()
		if err == nil {
			t.Errorf("--mark %q: no error", mark)
		}
	}

	r.At(testkit.Later, "checkout", "-q", "--detach")

	_, err := runApp(t, "-C", r.Dir, "pr", "catchup", "--mark", "f10-landed main").outErr()
	if err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Errorf("detached HEAD: %v", err)
	}
}
