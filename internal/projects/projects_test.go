package projects

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsName(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{value: "r3", want: true},
		{value: "notion-sdk-go", want: true},
		{value: "../r3", want: false},
		{value: "./r3", want: false},
		{value: "~/code/r3", want: false},
		{value: "/abs/r3", want: false},
		{value: "", want: false},
	}

	for _, c := range cases {
		if got := IsName(c.value); got != c.want {
			t.Errorf("IsName(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

// Off unless asked for: with the variable unset nothing is globbed and
// nothing is scanned, which is the fence this package exists to keep.
func TestResolveDisabledByDefault(t *testing.T) {
	t.Setenv(RootsEnv, "")

	if roots := Roots(); roots != nil {
		t.Errorf("Roots() with %s unset = %v, want nil", RootsEnv, roots)
	}

	_, err := Resolve("r3")
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("Resolve with %s unset = %v, want ErrDisabled", RootsEnv, err)
	}
}

func TestResolve(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"org-a/r3", "org-b/westside", "org-b/r3", "org-a/not-a-checkout"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// every candidate but one is a real checkout; a linked worktree carries
	// .git as a file, so both forms are made here
	for _, dir := range []string{"org-a/r3", "org-b/r3"} {
		if err := os.MkdirAll(filepath.Join(home, dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	linked := filepath.Join(home, "org-b", "westside", ".git")
	if err := os.WriteFile(linked, []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(RootsEnv, filepath.Join(home, "org-b", "*"))

	got, err := Resolve("westside")
	if err != nil {
		t.Fatalf("Resolve(westside): %v", err)
	}

	if want := filepath.Join(home, "org-b", "westside"); got != want {
		t.Errorf("Resolve(westside) = %q, want %q", got, want)
	}

	if _, err := Resolve("not-a-checkout"); err == nil {
		t.Error("Resolve matched a directory that is not a checkout")
	}

	// two roots, one name: a guess here would run against the wrong repo
	t.Setenv(RootsEnv, filepath.Join(home, "org-a", "*")+
		string(os.PathListSeparator)+filepath.Join(home, "org-b", "*"))

	_, err = Resolve("r3")
	if err == nil || !strings.Contains(err.Error(), "matches 2 checkouts") {
		t.Fatalf("Resolve with an ambiguous name = %v, want an error naming both", err)
	}
}
