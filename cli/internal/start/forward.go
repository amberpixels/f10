package start

import (
	"context"
	"fmt"
	"time"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// ForwardInput is everything forward needs once the target is settled, so
// the flow can be tested without a checkout.
type ForwardInput struct {
	Main     string                // the main checkout: worktrees are its siblings
	Here     string                // the checkout the caller answers for: a task here is not forwarded
	Driver   *trackerdriver.Driver // nil without one
	Task     ref.Ref
	Suffix   string
	Text     string        // what the agent is told, before the marker
	StateDir string        // where run state lives, for the idle check
	Ttl      time.Duration // how long a run counts as live
	Now      time.Time
	Since    time.Time // runs last updated before it are not read: the agent that wrote them is gone
}

// Forwarded is where a forward went: the workspace, the text as sent, and
// what the Enter check noted.
type Forwarded struct {
	Workspace string
	Sent      string
	Note      string
}

func (in ForwardInput) stall() StallCheck {
	return StallCheck{StateDir: in.StateDir, Ttl: in.Ttl, Now: in.Now, Since: in.Since}
}

// Forward hands text to the agent working on in.Task in another checkout.
// Every refusal comes before the one thing that leaves this process.
func Forward(ctx context.Context, in ForwardInput) (Forwarded, error) {
	name, path, err := stack.TaskCheckout(ctx, in.Main, in.Driver, in.Task, in.Suffix)
	if err != nil {
		return Forwarded{}, err
	}

	if reason := hereReason(in, name, path); reason != "" {
		return Forwarded{}, ui.Exit(ui.ExitDeclined, "%s", reason)
	}

	if err := agents.RequireAgents(); err != nil {
		return Forwarded{}, err
	}

	wss, err := agents.Agents(in.Main).Workspaces(ctx)
	if err != nil {
		return Forwarded{}, err
	}

	ws := worktree.WorkspaceAt(wss, path)
	if ws == nil {
		return Forwarded{}, fmt.Errorf("no %s workspace shows %s (branch %s): open it with f10 start %s",
			agents.Agents(in.Main).Name(), path, name, in.Task.ID)
	}

	if err := RefuseStalled(in.stall(), ws, agentctl.AgentName(in.Task.ID, in.Suffix, path), path); err != nil {
		return Forwarded{}, err
	}

	text := launch.Driven(in.Text)

	note, err := launch.PromptChecked(ctx, path, in.Task.ID, in.Suffix, text)
	if err != nil {
		return Forwarded{}, err
	}

	return Forwarded{Workspace: ws.ID, Sent: text, Note: note}, nil
}

func hereReason(in ForwardInput, name, path string) string {
	switch {
	case name == "":
		return in.Task.ID + " has no branch here: run it in this session"
	case path == "":
		return fmt.Sprintf("branch %s is checked out nowhere: run it in this session", name)
	case gitx.SamePath(path, in.Here):
		return fmt.Sprintf("branch %s is checked out here: run it in this session", name)
	}

	return ""
}
