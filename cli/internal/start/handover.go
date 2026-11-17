package start

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/amberpixels/f10/cli/internal/agentctl"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// Discussing is the branch of t's checkout when a discussion opened it,
// else "": a start run there hands the worktree on.
func Discussing(ctx context.Context, t *target.Target) (string, error) {
	dir := t.Layout.CheckoutRoot

	branch, err := gitx.Branch(ctx, dir)
	if err != nil || branch == "" {
		return "", err
	}

	marked, err := branchmeta.Get(ctx, dir, branch, branchmeta.Discuss)
	if err != nil || marked == "" {
		return "", err
	}

	return branch, nil
}

// A HandOver is a discussion's worktree given to the first group of the
// start that ends it: the branch and the agent take the lead's names, so
// the drive finds both as that task's.
type HandOver struct {
	Main, Path string // the main checkout, and the discussion's worktree
	Branch     string // the discussion's branch
	Task       ref.Ref
	Suffix     string
	Name       string // the branch name a start for Task gives
	Tracker    stack.Tracker
}

// PrepareHandOver settles the hand-off of the discussion's branch to the
// start's first token; every refusal happens here, before anything moves.
func PrepareHandOver(ctx context.Context, t *target.Target, branch, first string) (HandOver, error) {
	id, suffix := ref.SplitSuffix(first)

	r, err := t.Reference(ctx, id)
	if err != nil {
		return HandOver{}, err
	}

	r = r.Head()
	h := HandOver{
		Main:    cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		Path:    t.Layout.CheckoutRoot,
		Branch:  branch,
		Task:    r,
		Suffix:  suffix,
		Tracker: stack.TrackerFor(t),
	}

	parent, err := hasSubtasks(ctx, h.Tracker, r)
	if err != nil {
		return HandOver{}, err
	}

	if parent {
		return HandOver{}, fmt.Errorf("%s has subtasks: name the ones that continue in this worktree first", r.ID)
	}

	if h.Name, _, err = stack.BranchName(ctx, t.Driver(), r, suffix, worktree.TitleLookup(t, r.Number)); err != nil {
		return HandOver{}, err
	}

	if h.Name == branch {
		return h, nil
	}

	x, err := stack.LoadBranchIndex(ctx, h.Main)
	if err != nil {
		return HandOver{}, err
	}

	existing, err := x.Find(ctx, h.Name, r.ID, suffix, stack.PolicyLocalOrOrigin)
	if err != nil {
		return HandOver{}, err
	}

	if existing.Name != "" && existing.Name != branch {
		return HandOver{}, fmt.Errorf("%s already has branch %s: start it from the main checkout, "+
			"or put another group first", r.ID, existing.Name)
	}

	return h, nil
}

// Run renames the agent and the workspace, then the branch, and records
// the group's riders. A git failure after the renames puts them back, so a
// refused hand-off leaves the discussion as it was.
func (h HandOver) Run(ctx context.Context) ([]string, error) {
	b := agents.Agents(h.Path)

	ren, ok := b.(agentctl.Renamer)
	if !ok {
		return nil, fmt.Errorf("%s cannot rename an agent, so this worktree cannot continue as %s", b.Name(), h.Task.ID)
	}

	ws, agent, err := discussionAgent(ctx, b, h.Path)
	if err != nil {
		return nil, err
	}

	to := agentctl.AgentName(h.Task.ID, h.Suffix, h.Path)

	undo, err := h.rename(ctx, ren, ws, agent, to)
	if err != nil {
		return nil, err
	}

	if err := h.moveBranch(ctx); err != nil {
		return nil, errors.Join(err, undo(ctx))
	}

	note := "this worktree continues as " + h.Task.Label()
	if h.Name != h.Branch {
		note += fmt.Sprintf(": %s renamed to %s", h.Branch, h.Name)
	}

	return []string{note}, nil
}

// rename gives the agent and the workspace the task's names, and returns
// what puts them back.
func (h HandOver) rename(
	ctx context.Context, ren agentctl.Renamer, ws agentctl.Workspace, agent, to string,
) (func(context.Context) error, error) {
	var undo []func(context.Context) error

	back := func(ctx context.Context) error {
		var errs []error
		for _, u := range slices.Backward(undo) {
			errs = append(errs, u(ctx))
		}

		return errors.Join(errs...)
	}

	if agent != "" && agent != to {
		if err := ren.RenameAgent(ctx, agent, to); err != nil {
			return nil, fmt.Errorf("renaming agent %s to %s: %w", agent, to, err)
		}

		undo = append(undo, func(ctx context.Context) error { return ren.RenameAgent(ctx, to, agent) })
	}

	if ws.ID != "" && ws.Label != h.Name {
		if err := ren.RenameWorkspace(ctx, ws.ID, h.Name); err != nil {
			return nil, errors.Join(fmt.Errorf("renaming workspace %s to %s: %w", ws.Label, h.Name, err), back(ctx))
		}

		label := ws.Label
		undo = append(undo, func(ctx context.Context) error { return ren.RenameWorkspace(ctx, ws.ID, label) })
	}

	return back, nil
}

// moveBranch renames the branch, which moves its recorded facts with it,
// then writes the group's lead and riders over the discussion's.
func (h HandOver) moveBranch(ctx context.Context) error {
	if h.Name != h.Branch {
		if _, err := gitx.Exec(ctx, h.Main, "branch", "-m", h.Branch, h.Name); err != nil {
			return fmt.Errorf("renaming %s to %s: %w", h.Branch, h.Name, err)
		}
	}

	if len(h.Task.Riders()) > 0 {
		return recordBundle(ctx, h.Main, h.Name, h.Task)
	}

	for _, k := range []branchmeta.Key{branchmeta.Task, branchmeta.Riders} {
		if err := branchmeta.UnsetIfSet(ctx, h.Main, h.Name, k); err != nil {
			return err
		}
	}

	return nil
}

// discussionAgent is the workspace showing path and the agent in it, the
// agent "" when none runs or the backend lists none.
func discussionAgent(ctx context.Context, b agentctl.Backend, path string) (agentctl.Workspace, string, error) {
	wss, err := b.Workspaces(ctx)
	if err != nil {
		return agentctl.Workspace{}, "", err
	}

	var ws agentctl.Workspace

	for _, w := range wss {
		if w.Path == path {
			ws = w

			break
		}
	}

	l, ok := b.(agentctl.Lister)
	if ws.ID == "" || !ok {
		return ws, "", nil
	}

	running, err := l.Agents(ctx)
	if err != nil {
		return ws, "", err
	}

	for _, a := range running {
		if a.Workspace == ws.ID {
			return ws, a.Name, nil
		}
	}

	return ws, "", nil
}
