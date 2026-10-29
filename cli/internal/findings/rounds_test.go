package findings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const header = "task: GH-13\nround: %s\nreviewed: e6af491\nbase: main e6af491\nreviewer: local\nverdict: findings\n\n"

func round(n, body string) string {
	return strings.Replace(header, "%s", n, 1) + body
}

const (
	oneOpen = "## Findings\n\n### 1.1 Missing check\nwhere: a.go:1\ncategory: tests and docs\n\n" +
		"### 1.2 Wrong default\nwhere: b.go:2\ncategory: correctness and logic\nverdict: fix\nreason: done\n"
	oneSettled = "## Findings\n\n### 1.1 Missing check\nwhere: a.go:1\ncategory: tests and docs\nverdict: ask\n\n" +
		"### 1.2 Wrong default\nwhere: b.go:2\ncategory: correctness and logic\nverdict: skip\nreason: by design\n"
	oneClean = "## Categories\n\n- tests and docs: none found\n"
)

func TestRounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   []string // round bodies, 1.md first
		round   int
		decline Decline
		open    []Open
	}{
		{name: "none", round: 1},
		{
			name:    "round one unsettled",
			files:   []string{oneOpen},
			decline: Unsettled,
			open:    []Open{{ID: "1.1", Title: "Missing check", Why: "no verdict"}},
		},
		{name: "round one clean", files: []string{oneClean}, round: 2},
		{name: "round one settled", files: []string{oneSettled}, round: 2},
		{
			name: "round two names what waits",
			files: []string{oneSettled, "## Prior round\n\n" +
				"### 1.1 Missing check - fixed\nthe test exists\nwhere: a.go:1\n\n" +
				"### 1.2 Wrong default - explanation disputed\nthe reason misses the caller\nwhere: b.go:2\n\n" +
				"## Findings\n\n### 2.1 New leak\nwhere: c.go:3\ncategory: security and performance\nverdict: ask\n\n" +
				"### 2.2 No doc\nwhere: d.go:4\ncategory: tests and docs\n"},
			decline: Exhausted,
			open: []Open{
				{ID: "1.2", Title: "Wrong default", Why: "explanation disputed"},
				{ID: "2.1", Title: "New leak", Why: "ask"},
				{ID: "2.2", Title: "No doc", Why: "no verdict"},
			},
		},
		{
			name: "round two settled",
			files: []string{oneSettled, "## Prior round\n\n" +
				"### 1.1 Missing check - still open\nno test yet\nwhere: a.go:1\nverdict: fix\n\n" +
				"### 1.2 Wrong default - explanation accepted\nok\nwhere: b.go:2\n"},
			decline: Exhausted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			var paths []string

			for i, body := range tc.files {
				n := string(rune('1' + i))
				path := filepath.Join(dir, n+".md")

				if err := os.WriteFile(path, []byte(round(n, body)), 0o600); err != nil {
					t.Fatal(err)
				}

				paths = append(paths, path)
			}

			st, err := Rounds(dir, paths)
			if err != nil {
				t.Fatal(err)
			}

			if st.Round != tc.round || st.Decline != tc.decline || !reflect.DeepEqual(st.Open, tc.open) {
				t.Errorf("got round %d, decline %q, open %+v", st.Round, st.Decline, st.Open)
			}

			if tc.round > 0 && st.Path != filepath.Join(dir, string(rune('0'+tc.round))+".md") {
				t.Errorf("path %q", st.Path)
			}

			if len(paths) > 0 && st.Latest != paths[len(paths)-1] {
				t.Errorf("latest %q", st.Latest)
			}
		})
	}
}

func TestRoundsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "1.md")
	if err := os.WriteFile(path, []byte("# findings\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Rounds(filepath.Dir(path), []string{path}); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want one naming %s", err, path)
	}
}
