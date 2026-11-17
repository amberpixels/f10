package layout

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Loads still over the budget, each struck from this list by the FT-570 PR
// that brings it under, so the list only shrinks.
var overBudgetLoads = map[string]bool{
	"ci catchup": true,
}

// Claude Code cuts a Bash output past 30,000 characters, so every load a skill
// or ship makes stays under 25,000, leaving room for a project's own
// project.md and overlays, which this temp repo has none of.
func TestLoadsFitOneOutput(t *testing.T) {
	const budget = 25000

	script := scriptPath(t)
	root := filepath.Join(filepath.Dir(script), "..")

	var mds []string
	for _, pattern := range []string{"steps/*.md", "skills/*/SKILL.md"} {
		found, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("glob: %v", err)
		}

		mds = append(mds, found...)
	}

	files := map[string][]mdLine{}
	for _, f := range mds {
		rel, _ := filepath.Rel(root, f)
		files[rel] = readLines(t, root, rel)
	}

	repo := mkRepo(t)
	seen := map[string]bool{}

	for _, site := range loadSites(files) {
		key := strings.Join(site.steps, " ")
		if seen[key] {
			continue
		}

		seen[key] = true

		cmd := exec.Command("bash", append([]string{script}, site.steps...)...)
		cmd.Dir = repo
		cmd.Env = append(withoutPathAndHome(os.Environ()), "HOME="+t.TempDir(), "PATH="+pathWithoutF10())

		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("f10-load-project.sh %s: %v", key, err)
		}

		n := len([]rune(string(out)))

		switch {
		case n > budget && !overBudgetLoads[key]:
			t.Errorf(
				"%s: `f10-load-project.sh %s` prints %d characters, over the %d budget",
				site.where, key, n, budget,
			)
		case n <= budget && overBudgetLoads[key]:
			t.Errorf(
				"`f10-load-project.sh %s` prints %d characters, under the budget now: drop it from overBudgetLoads",
				key, n,
			)
		}
	}

	for key := range overBudgetLoads {
		if !seen[key] {
			t.Errorf("overBudgetLoads names %q, which no skill or ship loads: drop it", key)
		}
	}
}
