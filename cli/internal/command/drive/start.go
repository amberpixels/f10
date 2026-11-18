package drive

import (
	"context"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/target"
)

// StartCommand is the start verb. A task, a list, a range or a parent runs
// through its flow in a drive the daemon steers. A discussion or a
// picked-up PR opens one agent prompted once, because it waits for a person.
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
			"--discuss opens one agent in one worktree, which reads the tasks and waits: a parent's, showing its subtask tree, " +
			"or the first id's, reading every id listed. A start run in that worktree continues there as its first group, " +
			"the branch and agent renamed to the group's lead, and returns once the daemon has the drive. " +
			"A PR url or #n picks that PR up, and --task names its task when the PR names none. " +
			"Words after the ids are a note for that agent.\n" +
			"A start whose every task is closed, none with a branch, opens a discussion instead: " +
			"its report names what shipped each task and any open task one digit off.\n" +
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

	sa, err := drivectl.ParseStart(argv)
	if err != nil {
		return err
	}

	switch sa.Kind {
	case drivectl.FlowStart:
		if req := closedDiscussion(ctx, cmd, sa.Drive); req != nil {
			return startOnce(ctx, cmd, req)
		}

		return Run(ctx, cmd, argv)
	case drivectl.MaybePickup:
		t, err := target.For(ctx, cmd)
		if err != nil {
			return err
		}

		if pick, _, err := start.PickupFor(ctx, t, sa.Once.Token); err != nil {
			return err
		} else if pick == nil {
			return Run(ctx, cmd, argv)
		}
	case drivectl.DiscussStart, drivectl.PickupStart:
	}

	return startOnce(ctx, cmd, onceRequest(sa.Once))
}

// onceRequest is a discussion or a pickup as the API takes it.
func onceRequest(o drivectl.OnceArgs) *f10v1.StartRequest {
	return &f10v1.StartRequest{
		Mode: map[string]f10v1.StartMode{
			launch.ModeDiscuss: f10v1.StartMode_START_MODE_DISCUSS, launch.ModePlan: f10v1.StartMode_START_MODE_PLAN,
		}[o.Mode],
		Token: o.Token,
		Tasks: o.Tasks,
		Note:  o.Note,
		Flow:  o.Flow,
		Pace:  o.Pace,
		After: o.After,
		Base:  o.Base,
		Task:  o.Task,
		Agent: o.Agent,
		Model: o.Model,
	}
}

// closedDiscussion is the discussion a flow start becomes when every task
// it names is closed, nil otherwise.
func closedDiscussion(ctx context.Context, cmd *cli.Command, a drivectl.DriveArgs) *f10v1.StartRequest {
	// outside Herdr the drive refuses before anything is read
	if agents.RequireAgents() != nil {
		return nil
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return nil
	}

	sw := start.ClosedSwitch(ctx, t, a)
	if sw == nil {
		return nil
	}

	fmt.Fprintln(cmd.ErrWriter, sw.Note)

	return &f10v1.StartRequest{
		Mode:  f10v1.StartMode_START_MODE_DISCUSS,
		Token: sw.Token,
		Tasks: sw.Tasks,
		After: sw.After,
		Base:  sw.Base,
		Agent: sw.Agent,
	}
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
