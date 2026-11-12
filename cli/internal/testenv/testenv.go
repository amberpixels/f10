// Package testenv makes a test binary hermetic: HOME, the ledger, the asks
// and run dirs under one temp root, the developer's git config and shell
// environment ignored. It imports nothing from this module, so any package's
// tests may use it; cmd/f10's tests check its paths against the real ones.
package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// Root holds every user-level dir for the run, so a test that sets nothing
// still never reads or writes the developer's own HOME.
var Root string

// Cleared is the environment the developer's shell may set, which would
// otherwise leak into the tests. The dir overrides among it are cleared, not
// pinned, so they follow HOME and a test that moves HOME moves them too.
var Cleared = []string{
	"XDG_CONFIG_HOME", "F10_STATE_DIR", "F10_USER_DIR", "CLAUDE_CONFIG_DIR", "GH_CONFIG_DIR", "GLAB_CONFIG_DIR",
	"F10_DRIVE_FILE", "F10_AGENT_MODEL", "F10_AGENT_VIA", "F10_AGENT_SESSION",
	"F10_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "CLAUDECODE",
	"HERDR_ENV", "HERDR_WORKSPACE_ID",
	"F10_ROOTS", "F10_STATE_TTL", "F10_STATE_DEBUG",
	"F10_PLUGIN_ROOT", "CLAUDE_PLUGIN_ROOT",
	"F10_STANDUP_RECORD", "F10_BOARD_RECORD", "F10_STATUS_GLYPHS",
	"XDG_RUNTIME_DIR",
}

// Main sets the environment up, runs the tests and exits with their code.
func Main(m *testing.M) {
	cleanup := Setup()
	code := m.Run()

	cleanup()
	os.Exit(code)
}

// Setup makes Root and points the user-level dirs into it, returning what
// removes them. A TestMain that also swaps seams calls it before m.Run.
func Setup() func() {
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

	Root, err = filepath.EvalSymlinks(root)
	if err != nil {
		panic(err)
	}

	home := Home()

	for _, name := range Cleared {
		_ = os.Unsetenv(name)
	}

	for name, value := range map[string]string{
		"HOME":         home,
		"F10_LEDGER":   filepath.Join(Root, "f10.db"),
		"F10_ASKS_DIR": filepath.Join(Root, "asks"),
		"F10_RUN_DIR":  runDir,
		// the developer's git config never shapes a fixture
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
	} {
		_ = os.Setenv(name, value)
	}

	// an ask raised here notifies nobody
	if err := os.MkdirAll(UserDir(), 0o750); err != nil {
		panic(err)
	}

	if err := os.WriteFile(filepath.Join(UserDir(), "md"), []byte("---\nnotify: off\n---\n"), 0o600); err != nil {
		panic(err)
	}

	return func() {
		_ = os.RemoveAll(root)
		_ = os.RemoveAll(runDir)
	}
}

// Home is the HOME the tests run under.
func Home() string { return filepath.Join(Root, "home") }

// UserDir is the user's f10 dir under Home, where the notify switch lives.
func UserDir() string { return filepath.Join(Home(), ".f10") }
