package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/probe"
)

// prRepo is a clone of a bare origin, so origin/HEAD and pushed branches
// exist; origin is a path, so no host CLI applies and every read is git's.
func prRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)

	dir := filepath.Join(root, "repo")
	git(t, root, "clone", "-q", origin, dir)
	git(t, dir, "config", "user.email", "me@x.io")
	git(t, dir, "config", "user.name", "me")
	commitFile(t, dir, "main.txt")
	git(t, dir, "push", "-q", "origin", "main")
	git(t, dir, "remote", "set-head", "origin", "-a")

	return dir
}

func commitFile(t *testing.T, dir, name string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", name)
}

// stacked puts feat on a pushed base branch, recorded as start records it.
func stacked(t *testing.T) string {
	t.Helper()

	dir := prRepo(t)
	git(t, dir, "switch", "-q", "-c", "base")
	commitFile(t, dir, "base.txt")
	git(t, dir, "push", "-q", "origin", "base")
	git(t, dir, "switch", "-q", "-c", "feat")
	commitFile(t, dir, "feat.txt")
	git(t, dir, "config", "branch.feat."+cfgAfterBranch, "base")

	return dir
}

func runPR(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	app := newApp()
	app.Writer, app.ErrWriter = &out, &errOut

	err := app.Run(context.Background(), append([]string{"f10", "-C", dir, "pr"}, args...))

	return out.String(), errOut.String(), err
}

func TestPRBase(t *testing.T) {
	dir := stacked(t)

	if out, _, err := runPR(t, dir, "base"); err != nil || out != "base\n" {
		t.Errorf("stacked: base = %q, %v; want base", out, err)
	}

	git(t, dir, "config", "branch.feat."+cfgAfterBranch, "gone")

	out, errOut, err := runPR(t, dir, "base")
	if err != nil || out != "main\n" || !strings.Contains(errOut, "base gone is gone locally and on origin") {
		t.Errorf("gone: base = %q, %q, %v; want main and the gone base named", out, errOut, err)
	}

	git(t, dir, "config", "--unset", "branch.feat."+cfgAfterBranch)

	if out, _, err := runPR(t, dir, "base"); err != nil || out != "main\n" {
		t.Errorf("unrecorded: base = %q, %v; want main", out, err)
	}
}

// A PR cannot target a base origin lacks, so --origin refuses it.
func TestPRBaseOriginRefusesALocalBase(t *testing.T) {
	dir := prRepo(t)
	git(t, dir, "switch", "-q", "-c", "base")
	commitFile(t, dir, "base.txt")
	git(t, dir, "switch", "-q", "-c", "feat")
	git(t, dir, "config", "branch.feat."+cfgAfterBranch, "base")

	if _, _, err := runPR(t, dir, "base", "--origin"); err == nil || !strings.Contains(err.Error(), "not on origin") {
		t.Errorf("err = %v, want the base named as not on origin", err)
	}
}

// A stacked branch's diff holds its own work, uncommitted included, and
// none of the base task's commits.
func TestPRDiffStacked(t *testing.T) {
	dir := stacked(t)

	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runPR(t, dir, "diff", "--name-only")
	if err != nil || out != "feat.txt\nmain.txt\n" {
		t.Errorf("diff = %q, %v; want feat.txt and the uncommitted main.txt", out, err)
	}
}

// The base's unpushed commit the branch was cut from is the base's work,
// and origin's copy of the base moving ahead is not this change either.
func TestPRDiffForksFromTheNewerBase(t *testing.T) {
	dir := stacked(t)
	git(t, dir, "switch", "-q", "base")
	commitFile(t, dir, "unpushed.txt")
	git(t, dir, "switch", "-q", "-c", "feat2")
	commitFile(t, dir, "feat2.txt")
	git(t, dir, "config", "branch.feat2."+cfgAfterBranch, "base")

	out, _, err := runPR(t, dir, "diff", "--name-only")
	if err != nil || out != "feat2.txt\n" {
		t.Errorf("diff = %q, %v; want feat2.txt alone", out, err)
	}
}

func TestPRViewWithNoPR(t *testing.T) {
	dir := stacked(t)

	out, _, err := runPR(t, dir, "view")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"branch: feat\n", "base: base\n", "no open PR for feat\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
}

func TestPullNumberOf(t *testing.T) {
	for token, want := range map[string]int{
		"12":                             12,
		"#12":                            12,
		"!12":                            12,
		"https://github.com/o/r/pull/12": 12,
		"https://gitlab.example.com/g/r/-/merge_requests/7": 7,
		"FT-12":                            0,
		"https://github.com/o/r/issues/12": 0,
		"":                                 0,
	} {
		if got := pullNumberOf(token); got != want {
			t.Errorf("pullNumberOf(%q) = %d, want %d", token, got, want)
		}
	}
}

// Only an open request is the branch's: a closed one before it is history.
func TestOpenPullOfSkipsClosed(t *testing.T) {
	f := newFakes(t)
	f.script("gh pr list --head feat --state all --json number,state,headRefName,headRefOid,baseRefName,url,"+
		"author,createdAt,mergedAt,closedAt --limit 100",
		`[{"number":3,"state":"CLOSED","createdAt":"2026-10-02T00:00:00Z"},`+
			`{"number":5,"state":"OPEN","createdAt":"2026-10-01T00:00:00Z"}]`)

	tg := &target{dir: t.TempDir(), pb: &probe.Probes{CLI: probe.Signal{Value: "gh"}}}

	if n, err := openPullOf(t.Context(), tg, "feat"); err != nil || n != 5 {
		t.Errorf("openPullOf = %d, %v; want 5", n, err)
	}
}

func TestPatchFiles(t *testing.T) {
	patch := "diff --git a/x.go b/x.go\nindex 1..2\n--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/old.md b/new.md\nrename from old.md\n"

	if got := patchFiles(patch); got != "x.go\nnew.md" {
		t.Errorf("patchFiles = %q", got)
	}
}
