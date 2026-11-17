package start

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// A Request is a start as its caller words it: the task or PR, how far the
// agent runs, and the flow, pace, base and agent profile, any of them empty.
type Request struct {
	Token string // the task id, with an optional suffix, or a PR url or #n
	Mode  string // "" ships; launch.ModePlan or launch.ModeDiscuss stop earlier
	Flow  string
	Pace  string
	After string // the task this one builds on
	Base  string // a branch to start from
	Task  string // the task a picked-up PR is for
	Agent string
	Model string
	Note  string
}

// Prepare settles everything a start needs before anything is created:
// every refusal a request can meet happens here.
func Prepare(ctx context.Context, t *target.Target, req Request) (StartInput, error) {
	if err := RefuseFlags(req); err != nil {
		return StartInput{}, err
	}

	token := strings.TrimSpace(req.Token)
	if token == "" {
		return StartInput{}, errors.New("start needs a task id")
	}

	run, err := runplan.ResolveRun(ctx, t.Effective, runplan.RunInput{Pace: req.Pace})
	if err != nil {
		return StartInput{}, err
	}

	fl, err := run.Flow(req.Flow)
	if err != nil {
		return StartInput{}, err
	}

	pick, pr, err := PickupFor(ctx, t, token)
	if err != nil {
		return StartInput{}, err
	}

	note := strings.TrimSpace(req.Note)
	if err := RefuseNote(req.Mode, pick, note); err != nil {
		return StartInput{}, err
	}

	if err := RefuseLandedPlan(req.Mode, pick); err != nil {
		return StartInput{}, err
	}

	r, suffix, err := startRef(ctx, t, token, pick, pr, req)
	if err != nil {
		return StartInput{}, err
	}

	in := StartInput{
		Main:    cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		Driver:  t.Driver(),
		Task:    r,
		Suffix:  suffix,
		Mode:    req.Mode,
		Flow:    fl,
		Pace:    strings.ToLower(strings.TrimSpace(req.Pace)),
		Base:    req.Base,
		Storage: t.Layout.StorageRoot,
		Title:   worktree.TitleLookup(t, r.Number),
		Pick:    pick,
		Note:    note,
	}

	if pick == nil {
		in.Started = StartedMove(t)
	}

	if err := startDependency(ctx, t, &in, r, pick, req.After, req.Base); err != nil {
		return StartInput{}, err
	}

	if err := startProfile(ctx, t, &in, req); err != nil {
		return StartInput{}, err
	}

	return in, nil
}

// RefuseFlags refuses the exclusive pairs before anything is looked up.
func RefuseFlags(req Request) error {
	if req.Flow != "" && req.Mode != "" {
		return fmt.Errorf(
			"--flow %s and --%s are exclusive: --%s stops before ship, which runs the flow",
			req.Flow,
			req.Mode,
			req.Mode,
		)
	}

	if strings.TrimSpace(req.After) != "" && strings.TrimSpace(req.Base) != "" {
		return errors.New(
			"--base and --after are exclusive: one names a git ref, the other a task to depend on",
		)
	}

	return nil
}

// startRef is the task the run starts and its suffix: a picked-up PR's one
// task, else the token's task, widened to its bundle's lead.
func startRef(
	ctx context.Context,
	t *target.Target,
	token string,
	pick *hostops.Pickup,
	pr forge.PR,
	req Request,
) (ref.Ref, string, error) {
	task := strings.TrimSpace(req.Task)

	var (
		r      ref.Ref
		suffix string
		err    error
	)

	if task, err = PickedTask(t.Effective.IDPrefix, t.HostIssues(), pick, pr, task); err != nil {
		return ref.Ref{}, "", err
	}

	switch {
	case pick != nil:
		if err := RefusePickupFlags(req.Flow, req.After, req.Base); err != nil {
			return ref.Ref{}, "", err
		}

		if strings.Contains(task, ref.Separator) {
			return ref.Ref{}, "", fmt.Errorf("--task %s: a PR is picked up for one task, not a bundle", task)
		}

		if r, err = t.Reference(ctx, task); err != nil {
			return ref.Ref{}, "", fmt.Errorf("%s: %w", pick.URL, err)
		}

		r.Bundle = nil
	default:
		var id string

		id, suffix = ref.SplitSuffix(token)

		if r, err = t.Reference(ctx, id); err != nil {
			return ref.Ref{}, "", err
		}

		if suffix != "" && r.Bundle != nil {
			return ref.Ref{}, "", fmt.Errorf("%s ships in bundle %s, which takes no suffix", r.ID, r.Label())
		}

		// any member starts, or reopens, the whole bundle under its lead
		r = r.Head()
	}

	return r, suffix, nil
}

// startDependency fetches a picked-up PR, or settles the task this one is
// stacked on: --after, else the body's After: line unless --base named a ref.
func startDependency(
	ctx context.Context,
	t *target.Target,
	in *StartInput,
	r ref.Ref,
	pick *hostops.Pickup,
	after, base string,
) error {
	var err error

	switch {
	case pick != nil:
		if err := FetchPickup(ctx, in.Main, pick); err != nil {
			return err
		}
	case after != "":
		afterRef, err := t.Reference(ctx, after)
		if err != nil {
			return fmt.Errorf("--after: %w", err)
		}

		in.After = afterRef.Head().ID
	case base == "":
		if in.After, in.Notes, err = AfterFromBody(ctx, stack.TrackerFor(t), in.Main, r); err != nil {
			return err
		}
	}

	return nil
}

// startProfile resolves the agent profile from --agent or --model over the
// project's default.
func startProfile(ctx context.Context, t *target.Target, in *StartInput, req Request) error {
	flag, err := runplan.ProfileFlag(req.Agent, req.Model)
	if err != nil {
		return err
	}

	run, err := runplan.ResolveRun(ctx, t.Effective, runplan.RunInput{Pace: req.Pace, Profile: flag})
	if err != nil {
		return err
	}

	layers, err := run.Agents(ctx)
	if err != nil {
		return err
	}

	resolved := layers.ProfileFor(facts.AgentKeyDefault, "")
	in.Profile, in.ProfileFlagged = resolved.Profile, flag != ""
	in.Notes = append(resolved.Lines(runplan.ClaudeSettingsPath()), in.Notes...)

	return nil
}

// RefuseNote stops a note no prompt can carry: only the prompts that stop
// and wait are plain text, and a driven chain's arguments belong to its
// skills.
func RefuseNote(mode string, pick *hostops.Pickup, note string) error {
	if note == "" || mode == launch.ModeDiscuss || (pick != nil && mode != launch.ModePlan) {
		return nil
	}

	return fmt.Errorf("%q: a note goes to an agent that stops and waits, with --discuss or a PR picked up; "+
		"a driven run takes its instructions from the task", note)
}

// RefuseLandedPlan stops --plan on a merged or closed PR: its diff is no
// starting point once it landed, and going on from it is a task of its own.
func RefuseLandedPlan(mode string, pick *hostops.Pickup) error {
	if pick == nil || !pick.Landed() || mode != launch.ModePlan {
		return nil
	}

	return fmt.Errorf("--plan continues an open PR; %s is %s: start its task by id to go on from it",
		pick.URL, pick.State)
}

// RefusePickupFlags stops the flags a picked-up PR has no use for: its
// branch and base are the PR's, and nobody ships someone else's PR unread.
func RefusePickupFlags(flow, after, base string) error {
	switch {
	case after != "" || base != "":
		return errors.New("--after and --base do not apply to a PR: its branch and its base are the PR's")
	case flow != "":
		return errors.New(
			"--local and --flow do not apply to a PR: the agent summarises it and waits, or plans with --plan",
		)
	}

	return nil
}
