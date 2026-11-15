package agents_test

import (
	"errors"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/seam"
)

// In the daemon, no registered session means Herdr is unreachable, never
// reached through the environment the daemon was spawned with.
func TestNoRegisteredSessionRefusesHerdr(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	seam.Swap(t, &agents.SessionEnv, func() []string { return nil })

	b := agents.Agents("")
	if err := b.Available(); !agents.IsOutside(err) {
		t.Errorf("Available with no session = %v, want the outside-Herdr refusal", err)
	}

	// a call that skips Available refuses the same way instead of running herdr
	if err := b.Prompt(t.Context(), "gh-1", "hi"); !errors.Is(err, agents.ErrNoSession) || !agents.IsOutside(err) {
		t.Errorf("Prompt with no session = %v, want ErrNoSession", err)
	}
}
