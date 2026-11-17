package launch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/hostops"
)

// PromptAgentAs prompts the task's agent in the worktree at path, falling
// back to the bare name an agent started before the path hash still answers
// to, and names the agent that took the text.
func PromptAgentAs(ctx context.Context, path, id, suffix, text string) (string, error) {
	agent := agents.Agents(path)
	name := agentctl.AgentName(id, suffix, path)

	err := agent.Prompt(ctx, name, text)
	if errors.Is(err, agentctl.ErrAgentNotFound) {
		name = agentctl.BareAgentName(id, suffix)
		err = agent.Prompt(ctx, name, text)
	}

	return name, err
}

// Prompt is what an agent that waits for a person is told: a discussion,
// or a picked-up PR, which is never shipped unread. A task run through its
// flow is prompted by its drive, one skill at a time.
func Prompt(mode, id string, dep *Dependency, plan string, pick *hostops.Pickup, note string) string {
	switch {
	case pick != nil && mode == ModePlan:
		return Driven(fmt.Sprintf("/f10:plan %s continue PR %s: its diff is the starting point", id, pick.URL))
	case pick != nil:
		return pickupPrompt(id, plan, pick, note)
	}

	return discussPrompt(id, dep, plan, note)
}

// discussPrompt fetches the task and stops. It carries no driven marker:
// the user talks to this agent in its pane, and its questions belong there.
// `f10 task read` brings comments and images, so the prompt names neither.
func discussPrompt(id string, dep *Dependency, plan, note string) string {
	p := fmt.Sprintf("Run `f10 task read %s` and summarise what it asks in a few lines.", id)

	if plan != "" {
		p += " A plan for it is saved at " + plan + "; read it too."
	}

	if dep != nil {
		p += " For context: " + DependencyClause(id, dep) + "."
	}

	return p + noteClause(note) + " Then stop and wait."
}

// pickupPrompt is discussPrompt for a picked-up PR: the task, the PR's
// state and its diff summarised, then the agent waits in its own pane.
func pickupPrompt(id, plan string, pick *hostops.Pickup, note string) string {
	standing := "the PR's state (reviews, checks, open threads)"
	if pick.Landed() {
		standing = "that the PR is " + pick.State
	}

	p := fmt.Sprintf("Run `f10 task read %s`, `%s` and `%s`, and summarise in a few lines what the task asks, "+
		"%s and what its diff does, opening any image the PR links.",
		id, pick.View, pick.Diff, standing)

	if plan != "" {
		p += " A plan for it is saved at " + plan + "; read it too."
	}

	return p + noteClause(note) + " Then stop and wait."
}

// noteClause is the user's note as a sentence of the prompt, or "".
func noteClause(note string) string {
	if note == "" {
		return ""
	}

	return " Then, as the user asks: " + strings.TrimRight(note, ".") + "."
}

// DependencyClause names the base task and where its plan is. What to do
// with that (design against the plan, not this checkout, with no fallback
// for its absence) is the fetch step's rule, so the prompt does not repeat it.
func DependencyClause(id string, dep *Dependency) string {
	switch {
	case dep.Planned:
		return fmt.Sprintf("%s depends on %s, whose plan is at %s", id, dep.Task, dep.Plan)
	case dep.Plan != "":
		return fmt.Sprintf("%s depends on %s, whose plan is not written yet (expected at %s)", id, dep.Task, dep.Plan)
	default:
		return fmt.Sprintf("%s depends on %s, whose plan is out of reach here", id, dep.Task)
	}
}
