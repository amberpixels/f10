// Package start is the f10 start and f10 forward commands: it parses their
// arguments and hands the work to the start service.
package start

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agents"
	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/runplan"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// Command is the start verb, which replaces three hand-offs across three tools, each a place
// to lose the suffix, the base or the "keep it local" words. Outside Herdr
// it refuses before touching anything rather than degrade to a plain claude.
func Command() *cli.Command {
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
	ModeDefault = ""
)

func runStart(ctx context.Context, cmd *cli.Command) error {
	mode, flowName, after, base, err := startFlags(cmd)
	if err != nil {
		return err
	}

	if err := agents.RequireAgents(); err != nil {
		return err
	}

	if strings.TrimSpace(cmd.Args().First()) == "" {
		return errors.New("start needs a task id")
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	in, err := start.Prepare(ctx, t, start.Request{
		Token: cmd.Args().First(),
		Mode:  mode,
		Flow:  flowName,
		Pace:  cmd.String("pace"),
		After: after,
		Base:  base,
		Task:  cmd.String("task"),
		Agent: cmd.String("agent"),
		Model: cmd.String("model"),
		Note:  strings.Join(cmd.Args().Tail(), " "),
	})
	if err != nil {
		return err
	}

	in.Progress = cmd.ErrWriter

	if err := start.Start(ctx, cmd.Writer, in); err != nil {
		return err
	}

	afterStart(ctx, cmd, t, in.Task, in.Suffix)

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

	after, base := strings.TrimSpace(cmd.String("after")), strings.TrimSpace(cmd.String("base"))
	if err := start.RefuseFlags(start.Request{Mode: mode, Flow: flowName, After: after, Base: base}); err != nil {
		return "", "", "", "", err
	}

	return mode, flowName, after, base, nil
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

	drivectl.WatchProject(ctx, cmd.ErrWriter, t)

	finish.SweepIfDue(ctx, cmd.ErrWriter, filepath.Join(t.Layout.StorageRoot.Path(), finish.JanitorStamp), time.Now(),
		func(ctx context.Context) ([]string, error) { return finish.Janitor(ctx, finish.JanitorFor(t)) })
}

// RefuseNote is start.RefuseNote.
func RefuseNote(mode string, pick *hostops.Pickup, note string) error {
	return start.RefuseNote(mode, pick, note)
}

// RefuseLandedPlan is start.RefuseLandedPlan.
func RefuseLandedPlan(mode string, pick *hostops.Pickup) error {
	return start.RefuseLandedPlan(mode, pick)
}

// RefusePickupFlags is start.RefusePickupFlags.
func RefusePickupFlags(flow, after, base string) error {
	return start.RefusePickupFlags(flow, after, base)
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
		return ModeDefault, nil
	case 1:
		return strings.TrimPrefix(set[0], "--"), nil
	default:
		return "", fmt.Errorf("%s are exclusive: each names how far the agent runs", strings.Join(set, " and "))
	}
}
