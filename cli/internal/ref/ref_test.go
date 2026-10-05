package ref

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name       string
		prefix     string
		token      string
		wantID     string
		wantNumber string
		wantErr    bool
	}{
		{name: "bare number takes the project prefix", prefix: "GH", token: "22", wantID: "GH-22", wantNumber: "22"},
		{name: "hash prefix is stripped", prefix: "GH", token: "#22", wantID: "GH-22", wantNumber: "22"},
		{name: "declared id passes through", prefix: "ABC", token: "ABC-1042", wantID: "ABC-1042", wantNumber: "1042"},
		{name: "lowercase id is uppercased", prefix: "ABC", token: "abc-1042", wantID: "ABC-1042", wantNumber: "1042"},
		{name: "an explicit prefix beats the project's", prefix: "ABC", token: "GH-7", wantID: "GH-7", wantNumber: "7"},
		{name: "no prefix anywhere leaves the number alone", token: "22", wantID: "22", wantNumber: "22"},
		{
			name:       "an issue url ends in the number",
			prefix:     "GH",
			token:      "https://github.com/amberpixels/f10/issues/22",
			wantID:     "GH-22",
			wantNumber: "22",
		},
		{
			name:       "a gitlab work item url",
			prefix:     "GL",
			token:      "https://gitlab.com/g/p/-/work_items/7",
			wantID:     "GL-7",
			wantNumber: "7",
		},
		{
			name:       "a jira browse url carries its own prefix",
			prefix:     "GH",
			token:      "https://x.atlassian.net/browse/ABC-1042",
			wantID:     "ABC-1042",
			wantNumber: "1042",
		},
		{
			name:       "a url drops its query and fragment",
			prefix:     "GH",
			token:      "https://github.com/a/b/issues/5?x=1#top",
			wantID:     "GH-5",
			wantNumber: "5",
		},
		{
			name:       "a url with a trailing slash",
			prefix:     "GH",
			token:      "https://github.com/a/b/issues/5/",
			wantID:     "GH-5",
			wantNumber: "5",
		},
		{
			name:    "a url ending in prose is not a reference",
			prefix:  "GH",
			token:   "https://github.com/a/b/issues",
			wantErr: true,
		},
		{name: "prose is not a reference", prefix: "GH", token: "the search one", wantErr: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Resolver{Prefix: c.prefix}.parse(c.token, OriginExplicit)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parse(%q) = %+v, want an error", c.token, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("parse(%q): %v", c.token, err)
			}

			if got.ID != c.wantID || got.Number != c.wantNumber {
				t.Errorf("parse(%q) = %q/%q, want %q/%q", c.token, got.ID, got.Number, c.wantID, c.wantNumber)
			}
		})
	}
}

func TestSplitSuffix(t *testing.T) {
	cases := []struct {
		token      string
		wantBase   string
		wantSuffix string
	}{
		{token: "1454-attempt2", wantBase: "1454", wantSuffix: "attempt2"},
		{token: "1454_v2", wantBase: "1454", wantSuffix: "v2"},
		{token: "GH-1-attempt2", wantBase: "GH-1", wantSuffix: "attempt2"},
		{token: "#7-retry", wantBase: "#7", wantSuffix: "retry"},
		{token: "ABC-1042", wantBase: "ABC-1042"},
		{token: "22", wantBase: "22"},
		{token: "https://github.com/a/b/issues/5-x", wantBase: "https://github.com/a/b/issues/5-x"},
		{token: "GH-1-", wantBase: "GH-1-"},
	}

	for _, c := range cases {
		t.Run(c.token, func(t *testing.T) {
			base, suffix := SplitSuffix(c.token)
			if base != c.wantBase || suffix != c.wantSuffix {
				t.Errorf("SplitSuffix(%q) = %q/%q, want %q/%q", c.token, base, suffix, c.wantBase, c.wantSuffix)
			}
		})
	}
}

func TestMatchBranch(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		branch string
		want   string
	}{
		{name: "leading id", prefix: "ABC", branch: "ABC-2049/dark-mode", want: "ABC-2049"},
		{name: "embedded and lowercase", prefix: "ABC", branch: "feature/abc-123-fix-bug", want: "ABC-123"},
		{name: "no separator", prefix: "GH", branch: "gh22-quick", want: "GH-22"},
		{name: "prefix inside a word does not count", prefix: "ABC", branch: "newsABC-12", want: ""},
		{name: "no id in the branch", prefix: "GH", branch: "main", want: ""},
		{name: "no prefix, no match", branch: "ABC-2049/x", want: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchBranch(c.prefix, c.branch); got != c.want {
				t.Errorf("matchBranch(%q, %q) = %q, want %q", c.prefix, c.branch, got, c.want)
			}
		})
	}
}

// The cascade's precedence, exercised through the one path a test can drive
// end to end without a checkout: explicit beats everything, and the session
// answers when nothing else does.
func TestResolveCascade(t *testing.T) {
	dir := t.TempDir()
	state := []byte("v 1\ntask GH-22\nurl https://x\n")

	if err := os.WriteFile(filepath.Join(dir, "sess-1.state"), state, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("F10_STATE_DIR", dir)
	t.Setenv("F10_SESSION_ID", "sess-1")

	// no branch to find one in: Dir is a temp dir, not a checkout
	r := Resolver{Prefix: "GH", Dir: dir}

	got, err := r.Resolve(t.Context(), "")
	if err != nil {
		t.Fatalf("Resolve(\"\"): %v", err)
	}

	if got.ID != "GH-22" || got.Origin != OriginSession {
		t.Errorf("Resolve(\"\") = %q from %q, want GH-22 from session", got.ID, got.Origin)
	}

	got, err = r.Resolve(t.Context(), "7")
	if err != nil {
		t.Fatalf("Resolve(\"7\"): %v", err)
	}

	if got.ID != "GH-7" || got.Origin != OriginExplicit {
		t.Errorf("Resolve(\"7\") = %q from %q, want GH-7 from explicit", got.ID, got.Origin)
	}
}

func TestResolveExhausted(t *testing.T) {
	t.Setenv("F10_STATE_DIR", t.TempDir())
	t.Setenv("F10_SESSION_ID", "nobody")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	_, err := Resolver{Prefix: "GH", Dir: t.TempDir()}.Resolve(context.Background(), "")
	if !errors.Is(err, ErrNoReference) {
		t.Fatalf("Resolve with nothing to find = %v, want ErrNoReference", err)
	}
}
