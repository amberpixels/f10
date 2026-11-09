package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/asks"
	"github.com/amberpixels/f10/cli/internal/daemon"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// testRoot holds every user-level dir for the run, so a test that sets
// nothing still never reads or writes the developer's own HOME.
var testRoot string

// testStorage is the storage root of a fixture that names none. The fakes
// drop the agent grants it adds, so a test that does not care about storage
// scripts the agent start without them.
var testStorage storage.Root

// cleared is the environment the developer's shell may set, which would
// otherwise leak into the tests. The dir overrides among it are cleared, not
// pinned, so they follow HOME and a test that moves HOME moves them too.
var cleared = []string{
	"XDG_CONFIG_HOME", "F10_STATE_DIR", userDirEnv, "CLAUDE_CONFIG_DIR", "GH_CONFIG_DIR", "GLAB_CONFIG_DIR",
	daemon.NumberFileEnv, "F10_AGENT_MODEL", "F10_AGENT_VIA", "F10_AGENT_SESSION",
	"F10_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "CLAUDECODE",
	"HERDR_ENV", "HERDR_WORKSPACE_ID",
	"F10_ROOTS", "F10_STATE_TTL", "F10_STATE_DEBUG",
	"F10_PLUGIN_ROOT", "CLAUDE_PLUGIN_ROOT",
	"F10_STANDUP_RECORD", "F10_BOARD_RECORD", "F10_STATUS_GLYPHS",
	"XDG_RUNTIME_DIR",
}

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "f10-test-")
	if err != nil {
		panic(err)
	}

	// the daemon puts a unix socket here, and a socket path has about 100
	// bytes, more than a dir under macOS $TMPDIR leaves
	runDir, err := os.MkdirTemp("/tmp", "f10r")
	if err != nil {
		panic(err)
	}

	testRoot, err = filepath.EvalSymlinks(root)
	if err != nil {
		panic(err)
	}

	home := filepath.Join(testRoot, "home")
	testStorage = storage.Root(filepath.Join(testRoot, "storage"))

	for _, name := range cleared {
		_ = os.Unsetenv(name)
	}

	for name, value := range map[string]string{
		"HOME":         home,
		ledger.PathEnv: filepath.Join(testRoot, "f10.db"),
		"F10_ASKS_DIR": filepath.Join(testRoot, "asks"),
		"F10_RUN_DIR":  runDir,
		// the developer's git config never shapes a fixture
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
	} {
		_ = os.Setenv(name, value)
	}

	askNotifier = nil

	// a start or a drive run here hands nothing to a real daemon
	watchProject = func(context.Context, io.Writer, *target) {}

	code := m.Run()

	_ = os.RemoveAll(root)
	_ = os.RemoveAll(runDir)

	os.Exit(code)
}

// A new default path that escapes the temp root fails here, not by writing
// to the developer's ~/.claude/f10 or ~/.f10.
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
		"claude settings": claudeSettingsPath(),
	} {
		if !strings.HasPrefix(path, testRoot+string(filepath.Separator)) {
			t.Errorf("%s = %q, outside the test root %q", name, path, testRoot)
		}
	}

	if dir := daemon.RunDir(); !strings.HasPrefix(dir, "/tmp/f10r") {
		t.Errorf("daemon run dir = %q, want the short dir TestMain made", dir)
	}
}
