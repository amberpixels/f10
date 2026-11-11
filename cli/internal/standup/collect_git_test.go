package standup

import (
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/gitx/gitxtest"
)

// The git half of the upkeep rules against real git: a merge of main, a
// go.sum-only bump, code, then a rebase and an amend for range-diff to pair.
// The recorded fixtures cover real histories; this covers what none holds.

const (
	before = "2026-10-10T10:00:00Z"
	during = "2026-10-14T10:00:00Z"
	later  = "2026-10-14T12:00:00Z"
)

func newStandupRepo(t *testing.T) gitxtest.Repo {
	t.Helper()

	r := gitxtest.Clone(t, "work")
	r.At(before, "checkout", "-q", "-b", "main")

	r.Commit(before, "main.go", "package standup\n", "init")
	r.Commit(before, "go.sum", "a v1\n", "deps")
	r.At(before, "push", "-q", "-u", "origin", "main")
	r.At(before, "remote", "set-head", "origin", "main")

	for _, task := range []string{"FT-1/a", "FT-2/b", "FT-3/c"} {
		r.At(before, "checkout", "-q", "-b", task, "main")
		r.Commit(before, strings.ReplaceAll(task, "/", "-")+".go", "package standup // "+task+"\n", task+" start")
		r.At(before, "push", "-q", "-u", "origin", task)
	}

	// main moves on under someone else's name
	r.At(before, "checkout", "-q", "main")
	r.At(during, "-c", "user.email=other@x.io", "commit", "-q", "--allow-empty", "-m", "theirs")
	r.Commit(during, "other.go", "package standup // theirs\n", "theirs 2")
	r.At(during, "push", "-q", "origin", "main")

	r.At(during, "checkout", "-q", "FT-1/a")
	r.At(during, "merge", "-q", "--no-edit", "origin/main")
	r.At(during, "checkout", "-q", "FT-2/b")
	r.Commit(during, "go.sum", "a v2\n", "bump a")
	r.At(during, "checkout", "-q", "FT-3/c")
	r.Commit(during, "FT-3-c.go", "package standup // FT-3 more\n", "FT-3 more")

	for _, task := range []string{"FT-1/a", "FT-2/b", "FT-3/c"} {
		r.At(during, "push", "-q", "origin", task)
	}

	return r
}

func TestStandupOnRealGit(t *testing.T) {
	r := newStandupRepo(t)

	in, err := Collect(t.Context(), Source{Dir: r.Dir, Main: r.Dir, Prefix: "FT"}, standupSince)
	if err != nil {
		t.Fatal(err)
	}

	rep := Group(in)
	got := map[string]string{}

	for _, task := range rep.Tasks {
		got[task.ID] = task.Class

		for _, c := range task.Commits {
			got[c.Subject] = strings.TrimSpace(c.Class + " " + c.Upkeep)
		}
	}

	for item, want := range map[string]string{
		"FT-1": ClassUpkeep,
		"Merge remote-tracking branch 'origin/main' into FT-1/a": "upkeep base-merge",
		"FT-2":      ClassUpkeep,
		"bump a":    "upkeep files",
		"FT-3":      ClassWork,
		"FT-3 more": ClassWork,
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

	r.At(later, "checkout", "-q", "FT-3/c")
	beforeRebase := r.At(later, "rev-parse", "HEAD")
	r.At(later, "rebase", "-q", "origin/main")
	rebased := r.At(later, "rev-parse", "HEAD")

	r.Commit(later, "FT-3-c.go", "package standup // FT-3 changed\n", "FT-3 fixup")
	r.At(later, "reset", "-q", "--soft", "HEAD~2")
	r.At(later, "commit", "-q", "-m", "FT-3 more")
	amended := r.At(later, "rev-parse", "HEAD")

	for _, tc := range []struct {
		name, before, after string
		want                string
	}{
		{"rebase", beforeRebase, rebased, "true"},
		{"amend changing a patch", rebased, amended, "false"},
		{"old head unknown here", strings.Repeat("e", 40), rebased, "unknown"},
	} {
		got := "unknown"
		if p := samePatches(t.Context(), r.Dir, "origin/main", tc.before, tc.after); p != nil {
			got = map[bool]string{true: "true", false: "false"}[*p]
		}

		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, got, tc.want)
		}
	}
}
