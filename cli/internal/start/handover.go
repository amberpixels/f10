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

	// the branch was made from the discussed task's base, which another
	// lead would ship on top of
	rec, err := branchmeta.Load(ctx, h.Main, branch)
	if err != nil {
		return HandOver{}, err
	}

	if rec.AfterBranch != "" {
		return HandOver{}, fmt.Errorf("%s is built on %s, the base of the task it was opened for: "+
			"start %s from the main checkout, or put that task's group first", branch, rec.AfterBranch, r.ID)
	}

	return h, nil
}

// Run renames the agent and the workspace, then the branch, and records
// the group's lead and riders over the discussed task's facts. A failure
// puts back what it changed; on success, the returned func undoes the
// whole hand-off, for a drive the daemon then refuses.
func (h HandOver) Run(ctx context.Context) ([]string, func(context.Context) error, error) {
	b := agents.Agents(h.Path)

	ren, ok := b.(agentctl.Renamer)
	if !ok {
		return nil, nil, fmt.Errorf("%s cannot rename an agent, so this worktree cannot continue as %s",
			b.Name(), h.Task.ID)
	}

	ws, agent, err := discussionAgent(ctx, b, h.Path)
	if err != nil {
		return nil, nil, err
	}

	to := agentctl.AgentName(h.Task.ID, h.Suffix, h.Path)

	unname, err := h.rename(ctx, ren, ws, agent, to)
	if err != nil {
		return nil, nil, err
	}

	unmove, err := h.moveBranch(ctx)
	if err != nil {
		return nil, nil, errors.Join(err, unname(ctx))
	}

	undo := func(ctx context.Context) error { return errors.Join(unmove(ctx), unname(ctx)) }

	note := "this worktree continues as " + h.Task.Label()
	if h.Name != h.Branch {
		note += fmt.Sprintf(": %s renamed to %s", h.Branch, h.Name)
	}

	return []string{note}, undo, nil
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

// leadKeys are the facts a start records for its own task; another lead
// starts without the discussed task's.
var leadKeys = []branchmeta.Key{
	branchmeta.Task, branchmeta.Riders, branchmeta.After, branchmeta.AfterBranch, branchmeta.Tip,
	branchmeta.Moved, branchmeta.Landed, branchmeta.Agent, branchmeta.Flow, branchmeta.Pace, branchmeta.Drive,
}

// moveBranch renames the branch, which moves its recorded facts with it,
// then writes the group's lead and riders in place of the discussed task's.
// It returns what puts the name and the facts back; a failure midway puts
// them back itself.
func (h HandOver) moveBranch(ctx context.Context) (func(context.Context) error, error) {
	kept := map[branchmeta.Key]string{}

	for _, k := range leadKeys {
		v, err := branchmeta.Get(ctx, h.Main, h.Branch, k)
		if err != nil {
			return nil, err
		}

		kept[k] = v
	}

	renamed := h.Name != h.Branch
	if renamed {
		if _, err := gitx.Exec(ctx, h.Main, "branch", "-m", h.Branch, h.Name); err != nil {
			return nil, fmt.Errorf("renaming %s to %s: %w", h.Branch, h.Name, err)
		}
	}

	undo := func(ctx context.Context) error {
		var errs []error

		for _, k := range leadKeys {
			if kept[k] == "" {
				errs = append(errs, branchmeta.UnsetIfSet(ctx, h.Main, h.Name, k))
			} else {
				errs = append(errs, branchmeta.Set(ctx, h.Main, h.Name, k, kept[k]))
			}
		}

		if renamed {
			if _, err := gitx.Exec(ctx, h.Main, "branch", "-m", h.Name, h.Branch); err != nil {
				errs = append(errs, fmt.Errorf("renaming %s back to %s: %w", h.Name, h.Branch, err))
			}
		}

		return errors.Join(errs...)
	}

	if err := h.recordLead(ctx, renamed); err != nil {
		return nil, errors.Join(err, undo(ctx))
	}

	return undo, nil
}

// recordLead writes the group's lead and riders; another lead also drops
// the discussed task's flow, pace, profile and base facts.
func (h HandOver) recordLead(ctx context.Context, renamed bool) error {
	for _, k := range leadKeys {
		if !renamed && k != branchmeta.Task && k != branchmeta.Riders {
			continue
		}

		if err := branchmeta.UnsetIfSet(ctx, h.Main, h.Name, k); err != nil {
			return err
		}
	}

	if len(h.Task.Riders()) > 0 {
		return recordBundle(ctx, h.Main, h.Name, h.Task)
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
