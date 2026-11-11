package gitx

import (
	"os"
	"testing"
)

// The code under test commits, merges and rebases too, so the developer's
// git config (a signing key, a hook path) stays out of the whole package.
func TestMain(m *testing.M) {
	_ = os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	os.Exit(m.Run())
}
