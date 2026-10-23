package lookaround

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A round-two file's settled prior blocks are not open findings; one in a
// status that takes a verdict is.
func TestReadRootCountsOpenFindings(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "reviews", "FT-1")

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("1.md", "task: FT-1\nround: 1\nreviewed: a\nbase: main a\nreviewer: local\nverdict: findings\n\n"+
		"## Findings\n\n### 1.1 One\nsummary: s\nwhere: x.go:1\ncategory: correctness and logic\nverdict: fix\n")

	settled := "task: FT-1\nround: 2\nreviewed: b\nbase: main a\nreviewer: local\nverdict: clean\n\n" +
		"## Prior round\n\n### 1.1 One - fixed\nthe code shows it\n\n### 1.2 Two - explanation accepted\nfine\n\n" +
		"## Categories\n\n- correctness and logic: none found\n"

	write("2.md", settled)

	l := &Local{
		Findings: map[string]Findings{},
		Plans:    map[string]time.Time{},
		Archived: map[string]bool{},
		Verdicts: map[string]string{},
	}
	readRoot(root, l)

	if got := l.Findings["FT-1"]; got.Round != 2 || got.Open != 0 {
		t.Errorf("settled round two = %+v, want round 2 with nothing open", got)
	}

	reopened := strings.Replace(
		settled,
		"## Categories",
		"### 1.3 Three - still open\nstill there\nwhere: y.go:2\n\n## Categories",
		1,
	)
	write("2.md", reopened)

	l.Findings = map[string]Findings{}
	readRoot(root, l)

	if got := l.Findings["FT-1"]; got.Open != 1 {
		t.Errorf("reopened prior block = %+v, want one open", got)
	}
}
