package lore

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// numbered is n lines, "l01" to "ln", so a diff tells them apart.
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "l%02d\n", i)
	}

	return b.String()
}

func TestTrackAnchorToHead(t *testing.T) {
	r := newRepo(t)
	base := r.commit("base", map[string]string{"a.go": numbered(20), "gone.go": numbered(3)})
	git := CLIGit{Dir: r.dir}

	head := numbered(20)
	head = strings.Replace(head, "l01\n", "l01\nnew\nnew\n", 1) // two lines above the anchors
	head = strings.Replace(head, "l12\n", "l12 edited\n", 1)    // one line inside 10-14
	head = strings.Replace(head, "l17\nl18\n", "x\n", 1)        // all of 17-18 rewritten
	r.commit("edit", map[string]string{"a.go": head})
	r.git("rm", "-q", "gone.go")
	r.git("commit", "-q", "-m", "drop")

	hunks, err := git.Hunks(t.Context(), base, "a.go")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		anchor  Anchor
		now     string // empty: gone
		changed int
	}{
		{anchorAt("a.go", 10, 14, base, ""), "a.go:12-16", 1},
		{anchorAt("a.go", 5, 0, base, ""), "a.go:7", 0},
		{anchorAt("a.go", 1, 0, base, ""), "a.go:1", 0},
		{anchorAt("a.go", 17, 18, base, ""), "", 2},
		{anchorAt("a.go", 20, 0, base, ""), "a.go:21", 0},
	}

	for _, c := range cases {
		got := track(c.anchor, hunks)
		if !got.Checked || got.Changed != c.changed {
			t.Errorf("%s: checked %v, changed %d, want %d", c.anchor, got.Checked, got.Changed, c.changed)
		}

		switch {
		case c.now == "" && got.Now != nil:
			t.Errorf("%s: now %s, want gone", c.anchor, got.Now)
		case c.now != "" && (got.Now == nil || got.Now.String() != c.now):
			t.Errorf("%s: now %v, want %s", c.anchor, got.Now, c.now)
		}
	}

	if _, err := git.Hunks(t.Context(), base, "later.go"); err != nil {
		t.Errorf("a path neither side holds: %v", err)
	}

	r.commit("add", map[string]string{"later.go": numbered(10)})

	if _, err := git.Hunks(t.Context(), base, "later.go"); !errors.Is(err, ErrNotAtCommit) {
		t.Errorf("a file the anchor's commit lacks: %v, want ErrNotAtCommit", err)
	}

	if _, err := git.Hunks(t.Context(), base, "gone.go"); !errors.Is(err, ErrNoRecord) {
		t.Errorf("a file deleted at HEAD: %v, want ErrNoRecord", err)
	}

	for _, sha := range []string{"0123456789abcdef0123456789abcdef01234567", "--output=x", ""} {
		if _, err := git.Hunks(t.Context(), sha, "a.go"); !errors.Is(err, ErrNoCommit) {
			t.Errorf("commit %q: %v, want ErrNoCommit", sha, err)
		}
	}
}
