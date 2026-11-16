// Package start is the f10 start and f10 forward commands: it parses their
// arguments, calls the daemon's API and prints the reply.
package start

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/runplan"
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
	mode, err := startMode(cmd)
	if err != nil {
		return err
	}

	flowName, err := configcmd.FlowFlags(cmd)
	if err != nil {
		return err
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	caller, err := c.Caller(cmd)
	if err != nil {
		return err
	}

	res, err := c.Runs.Start(ctx, connect.NewRequest(&f10v1.StartRequest{
		Token:  cmd.Args().First(),
		Task:   cmd.String("task"),
		Mode:   modes[mode],
		Flow:   flowName,
		Pace:   cmd.String("pace"),
		After:  strings.TrimSpace(cmd.String("after")),
		Base:   strings.TrimSpace(cmd.String("base")),
		Agent:  cmd.String("agent"),
		Model:  cmd.String("model"),
		Note:   strings.Join(cmd.Args().Tail(), " "),
		Caller: caller,
	}))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	apiclient.Print(cmd.Writer, cmd.ErrWriter, res.Msg.GetPrinted())

	return nil
}

// modes are the start modes by the flag that names them.
var modes = map[string]f10v1.StartMode{
	ModeDefault:        f10v1.StartMode_START_MODE_SHIP,
	launch.ModePlan:    f10v1.StartMode_START_MODE_PLAN,
	launch.ModeDiscuss: f10v1.StartMode_START_MODE_DISCUSS,
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
