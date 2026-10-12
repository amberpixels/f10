package layout

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// conventions.sh walks a fixed list, not a glob, so a file added to
// conventions/ without a list entry would never load. This pins the two.
func TestConventionsListMatchesDir(t *testing.T) {
	bin := filepath.Dir(scriptPath(t))

	out, err := exec.Command(filepath.Join(bin, "conventions.sh")).Output()
	if err != nil {
		t.Fatalf("conventions.sh: %v", err)
	}

	var carried []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "carries: "); ok {
			carried = strings.Fields(rest)
		}
	}

	if strings.Contains(string(out), "MISSING - ") {
		t.Errorf("conventions.sh names a file that does not exist:\n%s", out)
	}

	files, err := filepath.Glob(filepath.Join(bin, "..", "conventions", "*.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var onDisk []string
	for _, f := range files {
		onDisk = append(onDisk, strings.TrimSuffix(filepath.Base(f), ".md"))
	}

	slices.Sort(onDisk)
	listed := slices.Sorted(slices.Values(carried))

	if !slices.Equal(listed, onDisk) {
		t.Errorf("conventions.sh carries %v, conventions/ holds %v", listed, onDisk)
	}
}
