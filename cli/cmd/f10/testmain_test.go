package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/testenv"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// A new default path that escapes the temp root fails here, not by writing
// to the developer's ~/.claude/f10 or ~/.f10. testenv names these dirs by
// their env vars alone, so this is where a renamed variable shows.
func TestUserDirsStayInTheTestRoot(t *testing.T) {
	userDir, err := storage.UserFilesDir()
	if err != nil {
		t.Fatal(err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"home":            home,
		"state dir":       state.Dir(),
		"asks dir":        asks.Dir(),
		"ledger":          ledger.Path(),
		"user f10 dir":    userDir,
		"claude settings": runplan.ClaudeSettingsPath(),
	} {
		if !strings.HasPrefix(path, testenv.Root+string(filepath.Separator)) {
			t.Errorf("%s = %q, outside the test root %q", name, path, testenv.Root)
		}
	}

	if dir := storage.UserDir(home); dir != testenv.UserDir() {
		t.Errorf("user dir = %q, want testenv's %q, where it turns notifications off", dir, testenv.UserDir())
	}

	if dir := daemon.RunDir(); !strings.HasPrefix(dir, "/tmp/f10r") {
		t.Errorf("daemon run dir = %q, want the short dir testenv made", dir)
	}

	for _, name := range []string{storage.UserDirEnv, daemon.NumberFileEnv} {
		if !slices.Contains(testenv.Cleared, name) {
			t.Errorf("testenv.Cleared lacks %s", name)
		}
	}

	if got := os.Getenv(ledger.PathEnv); got != filepath.Join(testenv.Root, "f10.db") {
		t.Errorf("%s = %q, want the ledger testenv set", ledger.PathEnv, got)
	}
}
