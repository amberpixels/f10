package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/testkit"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// retitle needs a template, and the host's issues to write it on.
func TestTaskRetitleRefuses(t *testing.T) {
	_, errOut, err := runAppStdin(t, strings.NewReader(""), "-C", createRepo(t, ""), "task", "retitle",
		"FT-12").streams()
	if err != nil || !strings.Contains(errOut, "declares no tracker.title") {
		t.Errorf("no template: err = %v, stderr %q", err, errOut)
	}

	repo := createRepo(t, "")
	md := "---\ntracker:\n  kind: notion\n  id: FT-###\n  title: \"{id}: {title}\"\n---\n# demo\n"

	if err := os.WriteFile(filepath.Join(repo, ".f10", "instructions", "project.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err = runAppStdin(t, strings.NewReader(""), "-C", repo, "task", "retitle", "--all").streams()
	if testkit.ExitCode(err) != ui.ExitDeclined {
		t.Errorf("notion tracker: err = %v, want exit %d", err, ui.ExitDeclined)
	}
}
