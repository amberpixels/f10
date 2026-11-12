package launch_test

import (
	"errors"
	"testing"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// A bare-named agent, started before names carried the path hash, still
// gets the prompt: the backend's not-found is what turns to the bare name.
func TestPromptAgentFallsBackToTheBareName(t *testing.T) {
	m := testkit.UseAgents(t)
	m.Started = []string{agentctl.BareAgentName("GH-1", "")}

	name, err := launch.PromptAgentAs(t.Context(), testkit.FwdWT, "GH-1", "", "hi")
	if err != nil || name != agentctl.BareAgentName("GH-1", "") {
		t.Fatalf("took %q, %v; want the bare name", name, err)
	}

	if _, err := launch.PromptAgentAs(
		t.Context(),
		testkit.FwdWT,
		"GH-2",
		"",
		"hi",
	); !errors.Is(
		err,
		agentctl.ErrAgentNotFound,
	) {
		t.Errorf("unknown agent = %v, want ErrAgentNotFound", err)
	}
}
