package agents_test

import (
	"testing"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/seam"
)

// In the daemon, no registered session means Herdr is unreachable, never
// reached through the environment the daemon was spawned with.
func TestNoRegisteredSessionRefusesHerdr(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	seam.Swap(t, &agents.SessionEnv, func() []string { return nil })

	if err := agents.Agents("").Available(); !agents.IsOutside(err) {
		t.Errorf("Available with no session = %v, want the outside-Herdr refusal", err)
	}
}
