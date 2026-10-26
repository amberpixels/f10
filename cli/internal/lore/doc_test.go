package lore

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The example change in docs/lore.md is the spec's; validate must pass on
// it as written there.
func TestDocExampleValidates(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "lore.md"))
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	files := docEntries(t, string(doc))

	if len(files) != 7 {
		t.Fatalf("the doc's example holds %d entries, want 7", len(files))
	}

	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s := &Store{Root: root}

	problems, err := s.Validate("FT-1")
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range problems {
		t.Error(p)
	}

	entries, err := s.Effective("FT-1")
	if err != nil {
		t.Fatal(err)
	}

	var kinds []Kind
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}

	if want := []Kind{KindIntent, KindDecision, KindFinding, KindVerdict, KindLesson}; !slices.Equal(kinds, want) {
		t.Errorf("effective kinds %v, want %v", kinds, want)
	}

	if res, err := s.Check("FT-1"); err != nil || !res.Pass() {
		t.Errorf("check: %v %v", res, err)
	}
}

var docEntryName = regexp.MustCompile("^`([A-Z0-9-]+/[0-9A-Z]{26}-[a-z]+\\.md)`$")

// docEntries lifts each path-labelled fenced block out of the doc.
func docEntries(t *testing.T, doc string) map[string]string {
	t.Helper()

	out := map[string]string{}

	var name, body string

	inside := false

	sc := bufio.NewScanner(strings.NewReader(doc))
	for sc.Scan() {
		line := sc.Text()

		switch {
		case inside && line == "```":
			out[name] = body
			name, inside = "", false
		case inside:
			body += line + "\n"
		case name != "" && strings.HasPrefix(line, "```"):
			body, inside = "", true
		default:
			if m := docEntryName.FindStringSubmatch(line); m != nil {
				name = m[1]
			}
		}
	}

	return out
}
