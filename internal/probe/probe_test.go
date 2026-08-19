package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostOf(t *testing.T) {
	cases := []struct{ url, want string }{
		{url: "git@github.com:amberpixels/f10.git", want: "github.com"},
		{url: "https://github.com/amberpixels/f10.git", want: "github.com"},
		{url: "https://user@gitlab.example.com/group/proj.git", want: "gitlab.example.com"},
		{url: "ssh://git@git.corp.io:2222/team/repo.git", want: "git.corp.io"},
		{url: "/Users/e/repos/bare.git", want: ""},
	}

	for _, tc := range cases {
		if got := hostOf(tc.url); got != tc.want {
			t.Errorf("hostOf(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestKnowsHost(t *testing.T) {
	dir := t.TempDir()

	// gh shape: hosts are top-level keys
	gh := filepath.Join(dir, "hosts.yml")
	writeFile(t, gh, "github.com:\n    user: someone\n    oauth_token: secret\n")

	// glab shape: hosts nested under `hosts:`
	glab := filepath.Join(dir, "config.yml")
	writeFile(t, glab, "hosts:\n  gitlab.com:\n    token: secret\n")

	cases := []struct {
		path, host string
		want       bool
	}{
		{path: gh, host: "github.com", want: true},
		{path: gh, host: "gitlab.com", want: false},
		{path: glab, host: "gitlab.com", want: true},
		{path: glab, host: "github.com", want: false},
		{path: filepath.Join(dir, "missing.yml"), host: "github.com", want: false},
		// the dot must not act as a regex wildcard
		{path: gh, host: "githubXcom", want: false},
	}

	for _, tc := range cases {
		if got := knowsHost(tc.path, tc.host); got != tc.want {
			t.Errorf("knowsHost(%q, %q) = %v, want %v", tc.path, tc.host, got, tc.want)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
