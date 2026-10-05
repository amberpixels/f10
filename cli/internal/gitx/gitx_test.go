package gitx

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// script answers Exec from a map keyed by the joined arguments and records
// every call, so a test asserts on what git was asked as much as on the
// result.
func script(t *testing.T, answers map[string]string) *[]string {
	t.Helper()

	var calls []string

	prev := Exec
	Exec = func(_ context.Context, _ string, args ...string) (string, error) {
		key := strings.Join(args, " ")
		calls = append(calls, key)

		out, ok := answers[key]
		if !ok {
			return "", errors.New("unscripted: git " + key)
		}

		return out, nil
	}

	t.Cleanup(func() { Exec = prev })

	return &calls
}

func TestOutSwallowsErrors(t *testing.T) {
	script(t, nil)

	if got := Out(t.Context(), ".", "rev-parse", "HEAD"); got != "" {
		t.Errorf("Out on a failing command = %q, want empty", got)
	}
}

func TestRefs(t *testing.T) {
	calls := script(t, map[string]string{
		"for-each-ref --format=%(refname) refs/heads/GH-1 refs/heads/GH-1-* refs/remotes/origin/GH-1": "refs/heads/GH-1/slug\nrefs/heads/GH-1-attempt2\nrefs/remotes/origin/GH-1\n",
	})

	got, err := Refs(t.Context(), ".", "refs/heads/GH-1", "refs/heads/GH-1-*", "refs/remotes/origin/GH-1")
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"GH-1/slug", "GH-1-attempt2", "origin/GH-1"}; !slices.Equal(got, want) {
		t.Errorf("Refs = %v, want %v", got, want)
	}

	if len(*calls) != 1 {
		t.Errorf("git called %d times, want 1", len(*calls))
	}
}

func TestParseWorktrees(t *testing.T) {
	out := `worktree /repo
HEAD abc
branch refs/heads/main

worktree /repo.GH-1-slug
HEAD def
branch refs/heads/GH-1/slug

worktree /repo.detached
HEAD 123
detached
`

	got := parseWorktrees(out)
	want := []Worktree{
		{Path: "/repo", Branch: "main"},
		{Path: "/repo.GH-1-slug", Branch: "GH-1/slug"},
		{Path: "/repo.detached"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("parseWorktrees = %+v, want %+v", got, want)
	}
}

func TestAddWorktree(t *testing.T) {
	cases := []struct {
		name   string
		base   string
		create bool
		want   string
	}{
		{name: "create from base", base: "origin/main", create: true, want: "worktree add -b GH-1 /p origin/main"},
		{name: "create from HEAD", create: true, want: "worktree add -b GH-1 /p"},
		{name: "existing branch", want: "worktree add /p GH-1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := script(t, map[string]string{c.want: ""})

			if err := AddWorktree(t.Context(), ".", "/p", "GH-1", c.base, c.create); err != nil {
				t.Fatal(err)
			}

			if (*calls)[0] != c.want {
				t.Errorf("git asked %q, want %q", (*calls)[0], c.want)
			}
		})
	}
}

func TestSetConfig(t *testing.T) {
	calls := script(t, map[string]string{"config branch.GH-1/slug.f10-after GH-7": ""})

	if err := SetConfig(t.Context(), ".", "branch.GH-1/slug.f10-after", "GH-7"); err != nil {
		t.Fatal(err)
	}

	if want := "config branch.GH-1/slug.f10-after GH-7"; (*calls)[0] != want {
		t.Errorf("git asked %q, want %q", (*calls)[0], want)
	}
}

func TestStatusListsEveryLine(t *testing.T) {
	script(t, map[string]string{"status --porcelain": " M a.go\n?? b.txt"})

	got, err := Status(t.Context(), ".")
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{" M a.go", "?? b.txt"}; !slices.Equal(got, want) {
		t.Errorf("Status = %q, want %q", got, want)
	}

	script(t, map[string]string{"status --porcelain": ""})

	if got, _ := Status(t.Context(), "."); got != nil {
		t.Errorf("Status on a clean tree = %q, want nil", got)
	}
}

func TestIsAncestorReadsTheExitCode(t *testing.T) {
	script(t, map[string]string{"merge-base --is-ancestor abc main": ""})

	if !IsAncestor(t.Context(), ".", "abc", "main") {
		t.Error("a scripted success read as not an ancestor")
	}

	if IsAncestor(t.Context(), ".", "def", "main") {
		t.Error("a failing merge-base read as an ancestor")
	}
}

func TestRemoteBranchExists(t *testing.T) {
	script(
		t,
		map[string]string{"ls-remote --heads origin GH-1": "abc\trefs/heads/GH-1", "ls-remote --heads origin GH-2": ""},
	)

	if ok, err := RemoteBranchExists(t.Context(), ".", "GH-1"); err != nil || !ok {
		t.Errorf("GH-1 = %v, %v; want true", ok, err)
	}

	if ok, err := RemoteBranchExists(t.Context(), ".", "GH-2"); err != nil || ok {
		t.Errorf("GH-2 = %v, %v; want false", ok, err)
	}
}

func TestConfigEntries(t *testing.T) {
	script(t, map[string]string{
		`config --get-regexp ^branch\..*\.f10-after-branch$`: "branch.GH-8/a.b.f10-after-branch GH-7/base\nbranch.GH-9.f10-after-branch main",
	})

	got := ConfigEntries(t.Context(), ".", `^branch\..*\.f10-after-branch$`)
	want := []ConfigEntry{
		{Key: "branch.GH-8/a.b.f10-after-branch", Value: "GH-7/base"},
		{Key: "branch.GH-9.f10-after-branch", Value: "main"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("entries = %+v, want %+v", got, want)
	}

	// no match is git's exit 1, which reads as an empty list
	if got := ConfigEntries(t.Context(), ".", "^nothing$"); got != nil {
		t.Errorf("entries for no match = %+v, want none", got)
	}
}

func TestUnsetConfig(t *testing.T) {
	calls := script(t, map[string]string{"config --unset branch.x.f10-after": ""})

	if err := UnsetConfig(t.Context(), ".", "branch.x.f10-after"); err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(*calls, "config --unset branch.x.f10-after") {
		t.Errorf("calls = %v", *calls)
	}
}
