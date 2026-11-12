package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// Writes need an open request: with no host there is none, and the branch
// is named.
func TestPRWritesNeedAnOpenPR(t *testing.T) {
	dir := testkit.PrRepo(t)

	_, _, err := runApp(t, "-C", dir, "pr", "edit", "--title", "x").streams()
	if err == nil || err.Error() != "no open PR for main" {
		t.Errorf("edit: err = %v", err)
	}

	_, _, err = runApp(t, "-C", dir, "pr", "edit").streams()
	if err == nil || !strings.Contains(err.Error(), "--title, --body-file") {
		t.Errorf("edit with nothing: err = %v", err)
	}

	err = runApp(t, "-C", dir, "pr", "status", "--name", "judge", "--state", "green", "-m", "x").err
	if err == nil || !strings.Contains(err.Error(), "success or failure") {
		t.Errorf("status with a bad state: err = %v", err)
	}
}

// create refuses before any host call when origin lacks the base or the
// branch at HEAD, naming the push that fixes it.
func TestPRCreateNeedsBothOnOrigin(t *testing.T) {
	body := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(body, []byte("the body\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := testkit.PrRepo(t)
	testkit.Git(t, dir, "switch", "-q", "-c", "base")
	testkit.CommitFile(t, dir, "base.txt")
	testkit.Git(t, dir, "switch", "-q", "-c", "feat")
	testkit.Git(t, dir, "config", branchmeta.AfterBranch.For("feat"), "base")

	if _, _, err := runApp(t, "-C", dir, "pr", "create", "--title", "T", "--body-file", body).streams(); err == nil ||
		!strings.Contains(err.Error(), "base base is not on origin") {
		t.Errorf("unpushed base: err = %v", err)
	}

	testkit.Git(t, dir, "push", "-q", "origin", "base")

	if _, _, err := runApp(t, "-C", dir, "pr", "create", "--title", "T", "--body-file", body).streams(); err == nil ||
		!strings.Contains(err.Error(), "git push -u origin feat") {
		t.Errorf("unpushed head: err = %v", err)
	}

	testkit.Git(t, dir, "push", "-q", "origin", "feat")
	testkit.CommitFile(t, dir, "feat.txt")

	if _, _, err := runApp(t, "-C", dir, "pr", "create", "--title", "T", "--body-file", body).streams(); err == nil ||
		!strings.Contains(err.Error(), "at HEAD") {
		t.Errorf("head ahead of origin: err = %v", err)
	}

	if _, _, err := runApp(t, "-C", dir, "pr", "create", "--body-file", body).streams(); err == nil ||
		!strings.Contains(err.Error(), "title") {
		t.Errorf("no title: err = %v", err)
	}
}
