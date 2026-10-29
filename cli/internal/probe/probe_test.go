package probe

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
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

func TestPlanNamesMostRecentFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)

	for i, name := range []string{"GH-1.md", "GH-2.md", "GH-3.md"} {
		path := filepath.Join(dir, name)
		writeFile(t, path, "plan")

		stamp := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	got := planNames(dir)
	want := []string{"GH-3.md", "GH-2.md", "GH-1.md"}

	if !slices.Equal(got, want) {
		t.Errorf("planNames = %v, want %v", got, want)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestVerifySignals(t *testing.T) {
	goMod := []Signal{{Value: "Go", Evidence: "go.mod"}}
	cases := []struct {
		name  string
		files map[string]string
		stack []Signal
		want  []Signal
	}{
		{
			name: "justfile with both recipes",
			files: map[string]string{"justfile": "mod cli\nset shell := [\"bash\"]\n\n# lint it\n" +
				"[group('qa')]\nlint: cli::lint\n    shellcheck x\n\n@test *args:\n    go test\n"},
			want: []Signal{{Value: "just lint / just test", Evidence: "justfile"}},
		},
		{
			name:  "justfile with one, a parameter default kept",
			files: map[string]string{"justfile": "test pkg=\"./...\":\n    go test {{pkg}}\nlint := \"x\"\n"},
			want:  []Signal{{Value: "just test", Evidence: "justfile"}},
		},
		{
			name:  "justfile with neither is a judgment, the stack unused",
			files: map[string]string{"justfile": "build:\n    go build\n"},
			stack: goMod,
			want: []Signal{
				{
					Value:    "the justfile has no lint or test entry: pick the ones that lint and test",
					Evidence: "justfile",
					Judgment: true,
				},
			},
		},
		{
			name: "Makefile targets, several on one line",
			files: map[string]string{
				"Makefile": ".PHONY: test check\nCC := gcc\nURL = http://x\ntest check: deps\n\tgo test\n",
			},
			want: []Signal{{Value: "make test / make check", Evidence: "Makefile"}},
		},
		{
			name:  "no build file, Go stack",
			stack: goMod,
			want: []Signal{
				{
					Value:    "no build file names them: the stack's idiomatic commands, e.g. go test ./... and the linter the repo configures",
					Evidence: "go.mod",
					Judgment: true,
				},
			},
		},
		{
			name:  "Ruby and its gemspec give one line",
			stack: []Signal{{Value: "Ruby", Evidence: "Gemfile"}, {Value: "Ruby gem", Evidence: "x.gemspec"}},
			want: []Signal{
				{
					Value:    "no build file names them: the stack's idiomatic commands, e.g. bundle exec rake or rspec, and rubocop where the repo configures it",
					Evidence: "Gemfile",
					Judgment: true,
				},
			},
		},
		{name: "nothing at all"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tc.files {
				writeFile(t, filepath.Join(dir, name), body)
			}

			if got := verifySignals(dir, tc.stack); !slices.Equal(got, tc.want) {
				t.Errorf("verifySignals = %+v, want %+v", got, tc.want)
			}
		})
	}
}
