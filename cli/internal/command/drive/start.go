package drive

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// StartCommand is the start verb. A task, a list, a range or a parent runs
// through its flow in a drive the daemon steers; a discussion and a
// picked-up PR open one agent, prompted once, since it waits for a person.
// Flags are parsed by hand, because a range is `GH-12 -- GH-15`.
func StartCommand() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "start tasks: each in a worktree, a Herdr workspace and an agent, run through the flow",
		ArgsUsage: "<id>[-suffix][:profile]... | <id>+<id>... | <id> -- <id> | <PR url | #n> [note...]  " +
			drive.DriveUsage() + " [--discuss] [--task TASK-ID]\n   f10 start --resume N " + drive.ResumeUsage(),
		Description: "Each task runs the default flow, or the one --flow or --local names (f10 flow lists them): " +
			"plan when the flow has a plan entry and the task no plan file, then ship, " +
			"then finish when the flow ends in it and the pace does not turn it off. A rerun keeps the flow a task's branch records.\n" +
			"--plan plans every task and implements none.\n" +
			"A parent runs its subtasks in After: order, its open drafts left out.\n" +
			"--pace NAME runs every task at that pace (f10 config lists them), recorded on each branch so a rerun keeps it; " +
			"a pace skips or tunes the flow's entries and never adds one.\n" +
			"A task's base is --after or --base, for one task, else its branch's recorded --after, else its body's After: line; " +
			"every task whose base branch has a commit runs, its finish waits for the base's, " +
			"and a halt stops only its dependents.\n" +
			"--parallel N caps the tasks running a skill at once (default: half the CPU cores, 2 to 8), " +
			"and the agents open to twice N; the rest queue in the start's order. A task on an ask, its base or " +
			"its finish frees its slot. A rerun or --resume keeps the cap unless it passes a new one.\n" +
			"--agent starts every agent on PROFILE (kind:model:effort, or a part), laid over the agents map's default; " +
			"--model is the model-only form; <id>:<profile> starts one task's agent on its own, which wins.\n" +
			"Each start keeps a drive record under the storage root, picked up by the same command or --resume N, " +
			"and holds a lease on each task: a task another drive holds is skipped, and f10 finish refuses it. " +
			"f10 status --drives lists the drives; f10 stop stops one.\n" +
			"The drive runs in the per-user f10 daemon, started when none runs: this command prints its progress, " +
			"Ctrl+C stops only that, and rerunning it follows the drive again. --foreground runs it in this process.\n" +
			"--discuss opens one task's agent, which summarises it and waits; a PR url or #n picks that PR up, " +
			"and --task names its task when the PR names none. Words after the id are a note for that agent.\n" +
			"Exit 0: every task went through its flow. 3: a picked-up PR names no task; rerun with --task. " +
			"4: an agent asks; rerun with --answer. " +
			"5: a task halted (a judge stop, a failure, an agent that stopped without reporting, its base halted).",
		SkipFlagParsing: true,
		Action:          runStart,
		ShellComplete:   configcmd.CompleteFlow,
	}
}

func runStart(ctx context.Context, cmd *cli.Command) error {
	argv := cmd.Args().Slice()
	if slices.Contains(argv, "--help") || slices.Contains(argv, "-h") {
		return cli.ShowSubcommandHelp(cmd)
	}

	once, err := oneShot(ctx, cmd, argv)
	if err != nil {
		return err
	}

	if once == nil {
		return Run(ctx, cmd, argv)
	}

	return startOnce(ctx, cmd, once)
}

// onceFlags are the flags a discussion or a pickup takes, each with whether
// it takes a value.
var onceFlags = map[string]bool{
	"--discuss": false, "--plan": false, "--local": false,
	"--flow": true, "--pace": true, "--after": true, "--base": true,
	"--task": true, "--agent": true, "--model": true,
}

// oneShot is the request when argv starts a discussion or picks up a PR,
// nil for tasks run through their flow. A bare #n asks the host, since it
// may be a PR or a task.
func oneShot(ctx context.Context, cmd *cli.Command, argv []string) (*f10v1.StartRequest, error) {
	named := slices.ContainsFunc(argv, func(tok string) bool {
		name, _, _ := strings.Cut(tok, "=")

		return name == "--discuss" || name == "--task"
	})

	req, words, err := parseOnce(argv)

	switch {
	case errors.Is(err, errDriveFlag) && !named:
		return nil, nil
	case err != nil:
		return nil, err
	case len(words) == 0 && named:
		return nil, errors.New("start needs a task id")
	case len(words) == 0:
		return nil, nil
	case !named:
		number, probe := worktree.PullToken(words[0])
		if number == "" {
			return nil, nil
		}

		if probe {
			t, err := target.For(ctx, cmd)
			if err != nil {
				return nil, err
			}

			if pick, _, err := start.PickupFor(ctx, t, words[0]); err != nil || pick == nil {
				return nil, err
			}
		}
	}

	req.Token, req.Note = words[0], strings.Join(words[1:], " ")

	return req, nil
}

// errDriveFlag is a flag only a flow start takes.
var errDriveFlag = errors.New("a flow start's flag")

// parseOnce reads argv as a one-shot start: the flags it takes, then the
// token and the note's words.
func parseOnce(argv []string) (*f10v1.StartRequest, []string, error) {
	req := &f10v1.StartRequest{}

	var words []string

	for i := 0; i < len(argv); i++ {
		name, value, inline := strings.Cut(argv[i], "=")

		takes, ok := onceFlags[name]
		if !ok {
			if strings.HasPrefix(name, "-") {
				return nil, nil, fmt.Errorf("%s: %w; --discuss and a PR pickup take one id", name, errDriveFlag)
			}

			words = append(words, argv[i])

			continue
		}

		if takes && !inline {
			if i+1 >= len(argv) {
				return nil, nil, fmt.Errorf("%s needs a value", name)
			}

			i++
			value = argv[i]
		}

		value = strings.TrimSpace(value)

		switch name {
		case "--discuss", "--plan":
			mode := map[string]f10v1.StartMode{
				"--discuss": f10v1.StartMode_START_MODE_DISCUSS, "--plan": f10v1.StartMode_START_MODE_PLAN,
			}[name]
			if req.GetMode() != f10v1.StartMode_START_MODE_UNSPECIFIED && req.GetMode() != mode {
				return nil, nil, errors.New("--plan and --discuss are exclusive: each names how far the agent runs")
			}

			req.Mode = mode
		case "--local":
			req.Flow = "local"
		case "--flow":
			req.Flow = value
		case "--pace":
			req.Pace = value
		case "--after":
			req.After = value
		case "--base":
			req.Base = value
		case "--task":
			req.Task = value
		case "--agent":
			req.Agent = value
		case "--model":
			req.Model = value
		}
	}

	return req, words, nil
}

// startOnce is a discussion or a pickup: one agent, prompted once, which the
// watcher looks after.
func startOnce(ctx context.Context, cmd *cli.Command, req *f10v1.StartRequest) error {
	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	if req.Caller, err = c.Caller(cmd); err != nil {
		return err
	}

	res, err := c.Runs.Start(ctx, connect.NewRequest(req))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	apiclient.Print(cmd.Writer, cmd.ErrWriter, res.Msg.GetPrinted())

	return nil
}
