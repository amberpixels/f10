package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/hostops"
)

// promptAgentAs prompts the task's agent in the worktree at path, falling
// back to the bare name an agent started before the path hash still answers
// to, and names the agent that took the text.
func promptAgentAs(ctx context.Context, path, id, suffix, text string) (string, error) {
	agent := agents(path)
	name := agentctl.AgentName(id, suffix, path)

	err := agent.Prompt(ctx, name, text)
	if errors.Is(err, agentctl.ErrAgentNotFound) {
		name = agentctl.BareAgentName(id, suffix)
		err = agent.Prompt(ctx, name, text)
	}

	return name, err
}

// prompt is what the agent is told. A saved plan, or a flow with no plan
// entry, turns plan-then-ship into ship alone. Driven mode takes every gap
// on its default and fetch carries the dependency's contract, so the prompt
// states neither; a picked-up PR is never shipped unread.
func prompt(
	mode string,
	fl shipFlow,
	id string,
	dep *dependency,
	plan string,
	pick *hostops.Pickup,
	note string,
) string {
	switch {
	case pick != nil && mode == modePlan:
		return driven(fmt.Sprintf("/f10:plan %s continue PR %s: its diff is the starting point", id, pick.URL))
	case pick != nil:
		return pickupPrompt(id, plan, pick, note)
	case mode == modeDiscuss:
		return discussPrompt(id, dep, plan, note)
	}

	var p string

	switch {
	case mode == modePlan:
		p = "/f10:plan " + id
	case plan != "" || !fl.plans():
		p = "/f10:ship " + fl.word() + id
	default:
		p = fmt.Sprintf("/f10:plan %s && /f10:ship %s%s", id, fl.word(), id)
	}

	if dep != nil {
		p += " && " + dependencyClause(id, dep)
	}

	// the agent is prompted from here, not by someone in its pane: driven
	// from its first turn, every question goes through run state
	return driven(p)
}

// discussPrompt fetches the task and stops. It carries no driven marker:
// the user talks to this agent in its pane, and its questions belong there.
// `f10 task read` brings comments and images, so the prompt names neither.
func discussPrompt(id string, dep *dependency, plan, note string) string {
	p := fmt.Sprintf("Run `f10 task read %s` and summarise what it asks in a few lines.", id)

	if plan != "" {
		p += " A plan for it is saved at " + plan + "; read it too."
	}

	if dep != nil {
		p += " For context: " + dependencyClause(id, dep) + "."
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

// dependencyClause names the base task and where its plan is. What to do
// with that (design against the plan, not this checkout, with no fallback
// for its absence) is the fetch step's rule, so the prompt does not repeat it.
func dependencyClause(id string, dep *dependency) string {
	switch {
	case dep.planned:
		return fmt.Sprintf("%s depends on %s, whose plan is at %s", id, dep.task, dep.plan)
	case dep.plan != "":
		return fmt.Sprintf("%s depends on %s, whose plan is not written yet (expected at %s)", id, dep.task, dep.plan)
	default:
		return fmt.Sprintf("%s depends on %s, whose plan is out of reach here", id, dep.task)
	}
}
