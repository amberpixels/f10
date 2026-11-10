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

	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/driverec"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
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
			configcmd.FlowFlag(),
			runplan.PaceFlag(),
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
		ShellComplete: configcmd.CompleteFlow,
	}
}

// --plan stops after the plan; --discuss after the fetch, for a task the
// user talks through in the agent's own pane.
const (
	modeDefault = ""
)

func runStart(ctx context.Context, cmd *cli.Command) error {
	mode, flowName, after, base, err := startFlags(cmd)
	if err != nil {
		return err
	}

	if err := worktree.Agents("").Available(); err != nil {
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
		main:    cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		driver:  t.Driver(),
		task:    r,
		suffix:  suffix,
		mode:    mode,
		flow:    fl,
		pace:    strings.ToLower(strings.TrimSpace(cmd.String("pace"))),
		base:    base,
		storage: t.Layout.StorageRoot,
		title:   worktree.TitleLookup(t, r.Number),
		pick:    pick,
		note:    note,
	}

	if checksParent(mode, pick) {
		in.children = stack.TrackerFor(t).Children
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

	if err := startProfile(ctx, cmd, t, &in); err != nil {
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

	flowName, err := configcmd.FlowFlags(cmd)
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
func startTarget(ctx context.Context, cmd *cli.Command, flowName string) (*target.Target, runplan.ShipFlow, error) {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return nil, runplan.ShipFlow{}, err
	}

	run, err := runplan.ResolveRun(ctx, t.Effective, runplan.RunInput{Pace: cmd.String("pace")})
	if err != nil {
		return nil, runplan.ShipFlow{}, err
	}

	fl, err := run.Flow(flowName)
	if err != nil {
		return nil, runplan.ShipFlow{}, err
	}

	return t, fl, nil
}

// startRef is the task the run starts and its suffix: a picked-up PR's one
// task, else the token's task, widened to its bundle's lead.
func startRef(
	ctx context.Context,
	cmd *cli.Command,
	t *target.Target,
	token string,
	pick *hostops.Pickup,
	pr forge.PR,
	flowName, after, base string,
) (ref.Ref, string, error) {
	task := strings.TrimSpace(cmd.String("task"))

	var (
		r      ref.Ref
		suffix string
		err    error
	)

	if task, err = pickedTask(t.Effective.IDPrefix, t.HostIssues(), pick, pr, task); err != nil {
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
	in *startInput,
	r ref.Ref,
	pick *hostops.Pickup,
	after, base string,
) error {
	var err error

	switch {
	case pick != nil:
		if err := fetchPickup(ctx, in.main, pick); err != nil {
			return err
		}
	case after != "":
		afterRef, err := t.Reference(ctx, after)
		if err != nil {
			return fmt.Errorf("--after: %w", err)
		}

		in.after = afterRef.Head().ID
	case base == "":
		if in.after, in.notes, err = afterFromBody(ctx, stack.TrackerFor(t), in.main, r); err != nil {
			return err
		}
	}

	return nil
}

// startProfile resolves the agent profile from --agent or --model over the
// project's default.
func startProfile(ctx context.Context, cmd *cli.Command, t *target.Target, in *startInput) error {
	flag, err := runplan.ProfileFlag(cmd.String("agent"), cmd.String("model"))
	if err != nil {
		return err
	}

	run, err := runplan.ResolveRun(ctx, t.Effective, runplan.RunInput{Pace: cmd.String("pace"), Profile: flag})
	if err != nil {
		return err
	}

	layers, err := run.Agents(ctx)
	if err != nil {
		return err
	}

	resolved := layers.ProfileFor(facts.AgentKeyDefault, "")
	in.profile, in.profileFlagged = resolved.Profile, flag != ""
	in.notes = append(resolved.Lines(runplan.ClaudeSettingsPath()), in.notes...)

	return nil
}

// afterStart records the launch for status, then runs start's upkeep: the
// watcher and the due janitor sweep.
func afterStart(ctx context.Context, cmd *cli.Command, t *target.Target, r ref.Ref, suffix string) {
	// the task is already open, so a failed launch record is a line, not a
	// failed start
	launched := state.Launch{Kind: state.LaunchStart, Value: r.Label() + worktree.SuffixOf(suffix)}
	if err := state.AppendLaunch(state.Dir(), state.Session(), launched); err != nil {
		ui.Warnf(cmd.ErrWriter, "recording the launch: %v", err)
	}

	driverec.WatchProject(ctx, cmd.ErrWriter, t)

	sweepIfDue(ctx, cmd.ErrWriter, filepath.Join(t.Layout.StorageRoot.Path(), janitorStamp), time.Now(),
		func(ctx context.Context) ([]string, error) { return janitor(ctx, janitorFor(t)) })
}

// refuseNote stops a note no prompt can carry: only the prompts that stop
// and wait are plain text, and a driven chain's arguments belong to its
// skills.
func refuseNote(mode string, pick *hostops.Pickup, note string) error {
	if note == "" || mode == launch.ModeDiscuss || (pick != nil && mode != launch.ModePlan) {
		return nil
	}

	return fmt.Errorf("%q: a note goes to an agent that stops and waits, with --discuss or a PR picked up; "+
		"a driven run takes its instructions from the task", note)
}

// refuseLandedPlan stops --plan on a merged or closed PR: its diff is no
// starting point once it landed, and going on from it is a task of its own.
func refuseLandedPlan(mode string, pick *hostops.Pickup) error {
	if pick == nil || !pick.Landed() || mode != launch.ModePlan {
		return nil
	}

	return fmt.Errorf("--plan continues an open PR; %s is %s: start its task by id to go on from it",
		pick.URL, pick.State)
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

	for _, m := range []string{launch.ModePlan, launch.ModeDiscuss} {
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
