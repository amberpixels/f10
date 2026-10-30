package gitx

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
			return "", &ExitError{Args: args, Code: 1}
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

	if ok, err := IsAncestor(t.Context(), ".", "abc", "main"); !ok || err != nil {
		t.Errorf("a scripted success = %v, %v, want an ancestor", ok, err)
	}

	if ok, err := IsAncestor(t.Context(), ".", "def", "main"); ok || err != nil {
		t.Errorf("merge-base exiting 1 = %v, %v, want no with no error", ok, err)
	}
}

func TestMergesCleanlyReadsTheExitCode(t *testing.T) {
	script(t, map[string]string{"merge-tree --write-tree origin/main origin/GH-1": ""})

	if ok, err := MergesCleanly(t.Context(), ".", "origin/main", "origin/GH-1"); !ok || err != nil {
		t.Errorf("a scripted clean merge = %v, %v, want clean", ok, err)
	}

	if ok, err := MergesCleanly(t.Context(), ".", "origin/main", "origin/GH-2"); ok || err != nil {
		t.Errorf("merge-tree exiting 1 = %v, %v, want a conflict with no error", ok, err)
	}
}

// MergesCleanly against a real git, since a conflict is read from the exit
// code alone and git's flags have changed what it means before.
func TestMergesCleanlyOnARealConflict(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()

		if _, err := Exec(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(body string) {
		t.Helper()

		if err := os.WriteFile(filepath.Join(dir, "f"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	write("base\n")
	run("add", "f")
	run("commit", "-qm", "base")
	run("branch", "side")
	run("branch", "other")
	write("main\n")
	run("commit", "-qam", "main")
	run("checkout", "-q", "side")
	write("side\n")
	run("commit", "-qam", "side")

	if ok, err := MergesCleanly(t.Context(), dir, "main", "side"); ok || err != nil {
		t.Errorf("a conflicting branch = %v, %v, want a conflict", ok, err)
	}

	if ok, err := MergesCleanly(t.Context(), dir, "main", "other"); !ok || err != nil {
		t.Errorf("a branch behind main = %v, %v, want clean", ok, err)
	}

	if _, err := MergesCleanly(t.Context(), dir, "main", "no-such-branch"); err == nil {
		t.Error("a bad ref read as a conflict, want an error")
	}
}

// Read tells "not there" from "git could not tell", against a real git: a
// missing key is absent, a config git cannot parse is an error.
func TestReadTellsAbsentFromFailed(t *testing.T) {
	dir := t.TempDir()

	if _, err := Exec(t.Context(), dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}

	if err := SetConfig(t.Context(), dir, "branch.x.f10-watch", "false"); err != nil {
		t.Fatal(err)
	}

	if v, err := Config(t.Context(), dir, "branch.x.f10-watch"); v != "false" || err != nil {
		t.Errorf("a set key = %q, %v", v, err)
	}

	if v, present, err := Read(
		t.Context(),
		dir,
		"config",
		"--get",
		"branch.y.f10-watch",
	); v != "" || present ||
		err != nil {
		t.Errorf("a missing key = %q, %v, %v, want absent with no error", v, present, err)
	}

	if sha, err := Ref(t.Context(), dir, "MERGE_HEAD"); sha != "" || err != nil {
		t.Errorf("no merge in progress = %q, %v, want absent with no error", sha, err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Config(t.Context(), dir, "branch.x.f10-watch"); err == nil {
		t.Error("an unparsable config read as absent, want an error")
	}

	if _, err := ConfigEntries(t.Context(), dir, "^branch\\."); err == nil {
		t.Error("an unparsable config listed no entries, want an error")
	}

	if _, err := IsAncestor(t.Context(), dir, "HEAD", "main"); err == nil {
		t.Error("ancestry in a broken repo read as no, want an error")
	}
}

func TestPushForcesOnlyWithALease(t *testing.T) {
	calls := script(t, map[string]string{
		"push origin HEAD:refs/heads/GH-1":                                        "",
		"push --force-with-lease=refs/heads/GH-1:abc origin HEAD:refs/heads/GH-1": "",
	})

	for _, lease := range []string{"", "abc"} {
		if err := Push(t.Context(), ".", "GH-1", lease); err != nil {
			t.Fatalf("lease %q: %v (calls %q)", lease, err, *calls)
		}
	}
}

func TestDeleteRemoteRefDropsOriginsCopy(t *testing.T) {
	calls := script(t, map[string]string{"update-ref -d refs/remotes/origin/GH-1": ""})

	if err := DeleteRemoteRef(t.Context(), ".", "GH-1"); err != nil || len(*calls) != 1 {
		t.Fatalf("err = %v, calls %q", err, *calls)
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

func TestRemoteBranches(t *testing.T) {
	script(t, map[string]string{
		"remote get-url origin":    "git@x:a/b.git",
		"ls-remote --heads origin": "abc\trefs/heads/GH-1\ndef\trefs/heads/feat/GH-2-x",
	})

	got, err := RemoteBranches(t.Context(), ".")
	if err != nil || len(got) != 2 || got["GH-1"] != "abc" || got["feat/GH-2-x"] != "def" {
		t.Errorf("RemoteBranches = %v, %v", got, err)
	}
}

// Without origin there is nothing to ask, and ls-remote is never run.
func TestRemoteBranchesWithoutOrigin(t *testing.T) {
	calls := script(t, map[string]string{})

	got, err := RemoteBranches(t.Context(), ".")
	if err != nil || len(got) != 0 || len(*calls) != 1 {
		t.Errorf("RemoteBranches = %v, %v, calls %q", got, err, *calls)
	}
}

func TestConfigEntries(t *testing.T) {
	script(t, map[string]string{
		`config --get-regexp ^branch\..*\.f10-after-branch$`: "branch.GH-8/a.b.f10-after-branch GH-7/base\nbranch.GH-9.f10-after-branch main",
	})

	got, err := ConfigEntries(t.Context(), ".", `^branch\..*\.f10-after-branch$`)
	if err != nil {
		t.Fatal(err)
	}

	want := []ConfigEntry{
		{Key: "branch.GH-8/a.b.f10-after-branch", Value: "GH-7/base"},
		{Key: "branch.GH-9.f10-after-branch", Value: "main"},
	}

	if !slices.Equal(got, want) {
		t.Errorf("entries = %+v, want %+v", got, want)
	}

	// no match is git's exit 1, which reads as an empty list
	if got, err := ConfigEntries(t.Context(), ".", "^nothing$"); got != nil || err != nil {
		t.Errorf("entries for no match = %+v, %v, want none", got, err)
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

// Conflicts reads merge-tree's stage lines on exit 1 into one Conflict per
// path; a clean merge is nil, and a loud exit is an error.
func TestConflictsNamesThePathsAndTheirBlobs(t *testing.T) {
	prev := Exec
	Exec = func(_ context.Context, _ string, args ...string) (string, error) {
		switch args[len(args)-1] {
		case "clean":
			return "tree", nil
		case "bad":
			return "", &ExitError{Args: args, Code: 1, Stderr: "fatal: not something we can merge"}
		default:
			return "", &ExitError{Args: args, Code: 1, Stdout: "tree\n" +
				"100644 b1 1\tb.go\n100644 b2 2\tb.go\n100644 b3 3\tb.go\n" +
				"100644 a2 2\ta.go\n100644 a3 3\ta.go\n"}
		}
	}

	t.Cleanup(func() { Exec = prev })

	if conflicts, err := Conflicts(t.Context(), ".", "main", "clean"); conflicts != nil || err != nil {
		t.Errorf("a clean merge = %v, %v, want nil", conflicts, err)
	}

	conflicts, err := Conflicts(t.Context(), ".", "main", "side")
	if err != nil || len(conflicts) != 2 ||
		conflicts[0].Key() != "a.go@/a2/a3" || conflicts[1].Key() != "b.go@b1/b2/b3" {
		t.Errorf("a conflict = %+v, %v, want a.go then b.go with their blobs", conflicts, err)
	}

	if _, err := Conflicts(t.Context(), ".", "main", "bad"); err == nil {
		t.Error("a bad ref read as a conflict, want an error")
	}
}

// Conflicts against a real git: the paths come from merge-tree's stdout,
// which Exec keeps on the exit error.
func TestConflictsOnARealConflict(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()

		if _, err := Exec(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, body string) {
		t.Helper()

		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	write("f", "base\n")
	write("g", "base\n")
	run("add", "f", "g")
	run("commit", "-qm", "base")
	run("branch", "side")
	write("f", "main\n")
	write("g", "main\n")
	run("commit", "-qam", "main")
	run("checkout", "-q", "side")
	write("f", "side\n")
	write("g", "side\n")
	run("commit", "-qam", "side")

	conflicts, err := Conflicts(t.Context(), dir, "main", "side")
	if err != nil || len(conflicts) != 2 || conflicts[0].Path != "f" || conflicts[1].Path != "g" {
		t.Fatalf("a conflicting branch = %+v, %v, want f,g", conflicts, err)
	}

	for _, c := range conflicts {
		if c.Stages[1] == "" || c.Stages[2] == "" || c.Stages[3] == "" || c.Stages[2] == c.Stages[3] {
			t.Errorf("%s: stages %q, want three distinct blobs", c.Path, c.Stages[1:])
		}
	}

	if conflicts, err := Conflicts(t.Context(), dir, "main", "main"); conflicts != nil || err != nil {
		t.Errorf("main into main = %v, %v, want clean", conflicts, err)
	}
}

// A pull that meets another git process holding origin's ref waits for it,
// against a real git: the lock file stands in for a concurrent pull.
func TestPullFFWaitsOutARefLock(t *testing.T) {
	root := t.TempDir()
	origin, clone := filepath.Join(root, "origin"), filepath.Join(root, "clone")
	run := func(dir string, args ...string) {
		t.Helper()

		if _, err := Exec(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}

	run(root, "init", "-q", "-b", "main", origin)
	run(origin, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "a")
	run(root, "clone", "-q", origin, clone)
	run(origin, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "b")

	lock := filepath.Join(clone, ".git", "refs", "remotes", "origin", "main.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	released := make(chan struct{})

	go func() {
		defer close(released)

		time.Sleep(300 * time.Millisecond)
		_ = os.Remove(lock)
	}()

	err := PullFF(t.Context(), clone)
	<-released

	if err != nil {
		t.Fatalf("PullFF with a briefly held ref = %v, want it to wait the lock out", err)
	}

	if Out(t.Context(), clone, "rev-parse", "HEAD") != Out(t.Context(), origin, "rev-parse", "HEAD") {
		t.Error("the clone did not reach origin's tip")
	}
}

// Only a lost ref lock retries: a pull that cannot fast-forward fails at once.
func TestPullFFFailsAtOnceOnAnythingElse(t *testing.T) {
	calls := script(t, map[string]string{})

	if err := PullFF(t.Context(), "/repo"); err == nil {
		t.Fatal("PullFF on a failing pull = nil")
	}

	if len(*calls) != 1 {
		t.Errorf("calls = %q, want one pull", *calls)
	}
}
