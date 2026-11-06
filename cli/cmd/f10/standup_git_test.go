package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/standup"
)

// The git half of the upkeep rules against real git: a merge of main, a
// go.sum-only bump, code, then a rebase and an amend for range-diff to pair.
// The recorded fixtures cover real histories; this covers what none holds.

const (
	before = "2026-10-10T10:00:00Z"
	during = "2026-10-14T10:00:00Z"
	later  = "2026-10-14T12:00:00Z"
)

func newStandupRepo(t *testing.T) gitRepo {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}

	r := cloneRepo(t, "work")
	r.at(before, "checkout", "-q", "-b", "main")

	r.commit(before, "main.go", "package main\n", "init")
	r.commit(before, "go.sum", "a v1\n", "deps")
	r.at(before, "push", "-q", "-u", "origin", "main")
	r.at(before, "remote", "set-head", "origin", "main")

	for _, task := range []string{"FT-1/a", "FT-2/b", "FT-3/c"} {
		r.at(before, "checkout", "-q", "-b", task, "main")
		r.commit(before, strings.ReplaceAll(task, "/", "-")+".go", "package main // "+task+"\n", task+" start")
		r.at(before, "push", "-q", "-u", "origin", task)
	}

	// main moves on under someone else's name
	r.at(before, "checkout", "-q", "main")
	r.at(during, "-c", "user.email=other@x.io", "commit", "-q", "--allow-empty", "-m", "theirs")
	r.commit(during, "other.go", "package main // theirs\n", "theirs 2")
	r.at(during, "push", "-q", "origin", "main")

	r.at(during, "checkout", "-q", "FT-1/a")
	r.at(during, "merge", "-q", "--no-edit", "origin/main")
	r.at(during, "checkout", "-q", "FT-2/b")
	r.commit(during, "go.sum", "a v2\n", "bump a")
	r.at(during, "checkout", "-q", "FT-3/c")
	r.commit(during, "FT-3-c.go", "package main // FT-3 more\n", "FT-3 more")

	for _, task := range []string{"FT-1/a", "FT-2/b", "FT-3/c"} {
		r.at(during, "push", "-q", "origin", task)
	}

	return r
}

func TestStandupOnRealGit(t *testing.T) {
	r := newStandupRepo(t)

	in, err := collectStandup(
		t.Context(),
		standupSource{dir: r.dir, main: r.dir, prefix: "FT", noHost: true},
		standupSince,
	)
	if err != nil {
		t.Fatal(err)
	}

	rep := standup.Group(in)
	got := map[string]string{}

	for _, task := range rep.Tasks {
		got[task.ID] = task.Class

		for _, c := range task.Commits {
			got[c.Subject] = strings.TrimSpace(c.Class + " " + c.Upkeep)
		}
	}

	for item, want := range map[string]string{
		"FT-1": standup.ClassUpkeep,
		"Merge remote-tracking branch 'origin/main' into FT-1/a": "upkeep base-merge",
		"FT-2":      standup.ClassUpkeep,
		"bump a":    "upkeep files",
		"FT-3":      standup.ClassWork,
		"FT-3 more": standup.ClassWork,
	} {
		if got[item] != want {
			t.Errorf("%s = %q, want %q (report %v)", item, got[item], want, got)
		}
	}
}

// A rebase onto a newer main keeps every patch; an amend that changes one
// does not; an old head never fetched here is unknown.
func TestSamePatchesOnRealGit(t *testing.T) {
	r := newStandupRepo(t)

	r.at(later, "checkout", "-q", "FT-3/c")
	beforeRebase := r.at(later, "rev-parse", "HEAD")
	r.at(later, "rebase", "-q", "origin/main")
	rebased := r.at(later, "rev-parse", "HEAD")

	r.commit(later, "FT-3-c.go", "package main // FT-3 changed\n", "FT-3 fixup")
	r.at(later, "reset", "-q", "--soft", "HEAD~2")
	r.at(later, "commit", "-q", "-m", "FT-3 more")
	amended := r.at(later, "rev-parse", "HEAD")

	for _, tc := range []struct {
		name, before, after string
		want                string
	}{
		{"rebase", beforeRebase, rebased, "true"},
		{"amend changing a patch", rebased, amended, "false"},
		{"old head unknown here", strings.Repeat("e", 40), rebased, "unknown"},
	} {
		got := "unknown"
		if p := samePatches(t.Context(), r.dir, "origin/main", tc.before, tc.after); p != nil {
			got = map[bool]string{true: "true", false: "false"}[*p]
		}

		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, got, tc.want)
		}
	}
}
