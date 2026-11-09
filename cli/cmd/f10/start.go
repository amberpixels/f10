package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/state"
)

// The start verb replaces three hand-offs across three tools, each a place
// to lose the suffix, the base or the "keep it local" words. Outside Herdr
// it refuses before touching anything rather than degrade to a plain claude.
func startCommand() *cli.Command {
	return &cli.Command{
		Name:      "start",
		Usage:     "start a task: a worktree, a Herdr workspace and a prompted agent",
		ArgsUsage: "<task-id>[-suffix] | <PR url | #n>  [note...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "plan", Usage: "prompt only the plan"},
			&cli.BoolFlag{
				Name:  "discuss",
				Usage: "prompt only the task's fetch: the agent summarises it and waits to discuss it before any plan",
			},
			&cli.BoolFlag{Name: "local", Usage: "ship through the project's local flow: --flow local"},
			flowFlag(),
			paceFlag(),
			&cli.StringFlag{
				Name:  "after",
				Usage: "base the branch on `TASK-ID`'s branch and record the dependency: ship rebases onto it, the PR stacks on it; default: the task body's After: line",
			},
			&cli.StringFlag{
				Name:  "base",
				Usage: "base the branch on `BRANCH`, local or on origin, instead of the default branch",
			},
			&cli.StringFlag{
				Name:  "task",
				Usage: "with a PR, the `TASK-ID` it belongs to, when its body, title and branch name none",
			},
			&cli.StringFlag{
				Name: "model",
				Usage: "start the agent on `MODEL`: a family (opus, fable), a version (opus-5.5) or a full id; " +
					"near misses are corrected, and a name that is no model falls back to claude's default",
			},
			&cli.StringFlag{
				Name: "agent",
				Usage: "start the agent on `PROFILE`, kind:model:effort (claude:opus:high), model:effort or either alone, " +
					"laid over the agents map's default; --model is the model-only form",
			},
		},
		Action:        runStart,
		ShellComplete: completeFlow,
	}
}

// --plan stops after the plan; --discuss after the fetch, for a task the
// user talks through in the agent's own pane.
const (
	modeDefault = ""
	modePlan    = "plan"
	modeDiscuss = "discuss"
)

func runStart(ctx context.Context, cmd *cli.Command) error {
	mode, flowName, after, base, err := startFlags(cmd)
	if err != nil {
		return err
	}

	if err := agents("").Available(); err != nil {
		return err
	}

	token := strings.TrimSpace(cmd.Args().First())
	if token == "" {
		return errors.New("start needs a task id")
	}

	t, fl, err := startTarget(ctx, cmd, flowName)
	if err != nil {
		return err
	}

	pick, pr, err := pickupFor(ctx, t, token)
	if err != nil {
		return err
	}

	note := strings.TrimSpace(strings.Join(cmd.Args().Tail(), " "))
	if err := refuseNote(mode, pick, note); err != nil {
		return err
	}

	if err := refuseLandedPlan(mode, pick); err != nil {
		return err
	}

	r, suffix, err := startRef(ctx, cmd, t, token, pick, pr, flowName, after, base)
	if err != nil {
		return err
	}

	in := startInput{
		main:     cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot),
		driver:   t.driver(),
		task:     r,
		suffix:   suffix,
		mode:     mode,
		flow:     fl,
		pace:     strings.ToLower(strings.TrimSpace(cmd.String("pace"))),
		base:     base,
		plansDir: t.sharedPlansDir(),
		title:    titleLookup(t, r.Number),
		pick:     pick,
		note:     note,
	}

	if checksParent(mode, pick) {
		in.children = t.tracker().children
	}

	if pick == nil {
		in.started = startedMove(t)
	}

	// before the body's After: line, whose own refusal would hide this one
	if err := refuseStartParent(ctx, in); err != nil {
		return err
	}

	if err := startDependency(ctx, t, &in, r, pick, after, base); err != nil {
		return err
	}

	if err := startProfile(cmd, t, &in); err != nil {
		return err
	}

	in.progress = cmd.ErrWriter

	if err := start(ctx, cmd.Writer, in); err != nil {
		return err
	}

	afterStart(ctx, cmd, t, r, suffix)

	return nil
}

// startFlags settles the mode and refuses the exclusive flag pairs before
// anything is looked up.
func startFlags(cmd *cli.Command) (string, string, string, string, error) {
	mode, err := startMode(cmd)
	if err != nil {
		return "", "", "", "", err
	}

	flowName, err := flowFlags(cmd)
	if err != nil {
		return "", "", "", "", err
	}

	if flowName != "" && mode != modeDefault {
		return "", "", "", "", fmt.Errorf(
			"--flow %s and --%s are exclusive: --%s stops before ship, which runs the flow",
			flowName,
			mode,
			mode,
		)
	}

	after, base := strings.TrimSpace(cmd.String("after")), strings.TrimSpace(cmd.String("base"))
	if after != "" && base != "" {
		return "", "", "", "", errors.New(
			"--base and --after are exclusive: one names a git ref, the other a task to depend on",
		)
	}

	return mode, flowName, after, base, nil
}

// startTarget reads the project and the flow the run's pace resolves.
func startTarget(ctx context.Context, cmd *cli.Command, flowName string) (*target, shipFlow, error) {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return nil, shipFlow{}, err
	}

	paces, err := paceSet(t.eff)
	if err != nil {
		return nil, shipFlow{}, err
	}

	pace, err := activePace(ctx, paces, cmd.String("pace"), "", "")
	if err != nil {
		return nil, shipFlow{}, err
	}

	fl, err := resolvePacedFlow(t.eff, paces, pace.name, flowName)
	if err != nil {
		return nil, shipFlow{}, err
	}

	return t, fl, nil
}

// startRef is the task the run starts and its suffix: a picked-up PR's one
// task, else the token's task, widened to its bundle's lead.
func startRef(
	ctx context.Context,
	cmd *cli.Command,
	t *target,
	token string,
	pick *pickup,
	pr pull,
	flowName, after, base string,
) (ref.Ref, string, error) {
	task := strings.TrimSpace(cmd.String("task"))

	var (
		r      ref.Ref
		suffix string
		err    error
	)

	if task, err = pickedTask(t.eff.IDPrefix, t.hostIssues(), pick, pr, task); err != nil {
		return ref.Ref{}, "", err
	}

	switch {
	case pick != nil:
		if err := refusePickupFlags(flowName, after, base); err != nil {
			return ref.Ref{}, "", err
		}

		if strings.Contains(task, ref.Separator) {
			return ref.Ref{}, "", fmt.Errorf("--task %s: a PR is picked up for one task, not a bundle", task)
		}

		if r, err = t.reference(ctx, task); err != nil {
			return ref.Ref{}, "", fmt.Errorf("%s: %w", pick.url, err)
		}

		r.Bundle = nil
	default:
		var id string

		id, suffix = ref.SplitSuffix(token)

		if r, err = t.reference(ctx, id); err != nil {
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
	t *target,
	in *startInput,
	r ref.Ref,
	pick *pickup,
	after, base string,
) error {
	var err error

	switch {
	case pick != nil:
		if err := fetchPickup(ctx, in.main, pick); err != nil {
			return err
		}
	case after != "":
		afterRef, err := t.reference(ctx, after)
		if err != nil {
			return fmt.Errorf("--after: %w", err)
		}

		in.after = afterRef.Head().ID
	case base == "":
		if in.after, in.notes, err = afterFromBody(ctx, t.tracker(), in.main, r); err != nil {
			return err
		}
	}

	return nil
}

// startProfile resolves the agent profile from --agent or --model over the
// project's default.
func startProfile(cmd *cli.Command, t *target, in *startInput) error {
	flag, err := profileFlag(cmd.String("agent"), cmd.String("model"))
	if err != nil {
		return err
	}

	layers, err := agentLayers(t.eff, flag)
	if err != nil {
		return err
	}

	resolved := layers.profileFor(facts.AgentKeyDefault, "")
	in.profile, in.profileFlagged = resolved.Profile, flag != ""
	in.notes = append(resolved.lines(claudeSettingsPath()), in.notes...)

	return nil
}

// afterStart records the launch for status, then runs start's upkeep: the
// watcher and the due janitor sweep.
func afterStart(ctx context.Context, cmd *cli.Command, t *target, r ref.Ref, suffix string) {
	// the task is already open, so a failed launch record is a line, not a
	// failed start
	launch := state.Launch{Kind: state.LaunchStart, Value: r.Label() + suffixOf(suffix)}
	if err := state.AppendLaunch(state.Dir(), state.Session(), launch); err != nil {
		fmt.Fprintf(cmd.ErrWriter, "f10: recording the launch: %v\n", err)
	}

	watchProject(ctx, cmd.ErrWriter, t)

	sweepIfDue(ctx, cmd.ErrWriter, filepath.Join(t.lay.StorageRoot, janitorStamp), time.Now(),
		func(ctx context.Context) ([]string, error) { return janitor(ctx, janitorFor(t)) })
}

// refuseNote stops a note no prompt can carry: only the prompts that stop
// and wait are plain text, and a driven chain's arguments belong to its
// skills.
func refuseNote(mode string, pick *pickup, note string) error {
	if note == "" || mode == modeDiscuss || (pick != nil && mode != modePlan) {
		return nil
	}

	return fmt.Errorf("%q: a note goes to an agent that stops and waits, with --discuss or a PR picked up; "+
		"a driven run takes its instructions from the task", note)
}

// refuseLandedPlan stops --plan on a merged or closed PR: its diff is no
// starting point once it landed, and going on from it is a task of its own.
func refuseLandedPlan(mode string, pick *pickup) error {
	if pick == nil || !pick.landed() || mode != modePlan {
		return nil
	}

	return fmt.Errorf("--plan continues an open PR; %s is %s: start its task by id to go on from it",
		pick.url, pick.state)
}

// refusePickupFlags stops the flags a picked-up PR has no use for: its
// branch and base are the PR's, and nobody ships someone else's PR unread.
func refusePickupFlags(flow, after, base string) error {
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

func startMode(cmd *cli.Command) (string, error) {
	var set []string

	for _, m := range []string{modePlan, modeDiscuss} {
		if cmd.Bool(m) {
			set = append(set, "--"+m)
		}
	}

	switch len(set) {
	case 0:
		return modeDefault, nil
	case 1:
		return strings.TrimPrefix(set[0], "--"), nil
	default:
		return "", fmt.Errorf("%s are exclusive: each names how far the agent runs", strings.Join(set, " and "))
	}
}
