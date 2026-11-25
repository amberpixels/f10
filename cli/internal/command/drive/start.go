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
// through its flow in a batch the daemon steers. A discussion or a
// picked-up PR opens one agent prompted once, because it waits for a person.
// Flags are parsed by hand, because a range is `GH-12 -- GH-15`.
func StartCommand() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "start tasks: each in a worktree, a Herdr workspace and an agent, run through the flow",
		ArgsUsage: "<id>[-suffix][@<base>...][:profile]... | <id>+<id>... | <id>,<id>... | <id> -- <id> | " +
			"<PR url | #n> [note...]  " +
			drive.DriveUsage() + " [--discuss] [--task TASK-ID]\n" +
			"   f10 start <id>... | <parent> --propose [--facts FILE] [--json] [--hint]",
		Description: "Each task runs the default flow, or the one --flow or --local names (f10 flow lists them): " +
			"plan when the flow has a plan entry and the task no plan file, then ship, " +
			"then finish when the flow ends in it and the pace does not turn it off. A rerun keeps the flow a task's branch records.\n" +
			"--plan plans every task and implements none.\n" +
			"A parent runs its subtasks in After: order, its open drafts left out.\n" +
			"--pace NAME runs every task at that pace (f10 config lists them), recorded on each branch so a rerun keeps it; " +
			"a pace skips or tunes the flow's entries and never adds one.\n" +
			"A task's base is --after or --base, for one task, else its branch's recorded base, else the order typed, " +
			"else its body's After: line; every task whose base branch has a commit runs, its finish waits for the base's, " +
			"and a halt stops only its dependents.\n" +
			"The order typed: A,B stacks B on A (A,B,C is A B@A C@B), C@A names A as C's base, and a space means none. " +
			"C@A@B is a join: it waits until A and B have merged, then branches from the default branch, " +
			"and a flow without finish refuses it. A profile comes last (C@A:opus). Nothing is written to the tracker.\n" +
			"--parallel N caps the tasks running a skill at once (default: half the CPU cores, 2 to 8), " +
			"and the agents open to twice N; the rest queue in the start's order. A task on an ask, its base or " +
			"its finish frees its slot. A rerun or f10 resume keeps the cap unless it passes a new one.\n" +
			"--agent starts every agent on PROFILE (kind:model:effort, or a part), laid over the agents map's default; " +
			"--model is the model-only form; <id>:<profile> starts one task's agent on its own, which wins.\n" +
			"The tasks one start runs are a batch, kept as a record under the storage root. The same command picks it up, " +
			"and so does f10 resume <any of its tasks>. The batch holds a lease on each task: a task another batch " +
			"holds is skipped, and f10 finish refuses it. " +
			"f10 status --batches lists the batches; f10 stop <task> stops one.\n" +
			"The batch runs in the per-user f10 daemon, started when none runs: this command prints its progress, " +
			"Ctrl+C stops only that, and rerunning it follows the batch again. --foreground runs it in this process.\n" +
			"--propose starts nothing: it prints a proposal for the tasks, which is the bundles, the order, " +
			"and the one command to run, the order spelled in it with , and @; it takes no typed order. Small tasks of one kind sharing files or an area " +
			"are bundled under bundle_max_lines, and the rest stay alone with the reason. --facts FILE lays the " +
			"start skill's estimates on top, --json prints the proposal for a program, with a schema number, and --hint prints one line " +
			"when a bundle saves a CI wait, else nothing.\n" +
			"--discuss opens one agent in one worktree, which reads the tasks and waits: a parent's, showing its subtask tree, " +
			"or the first id's, reading every id listed. A start run in that worktree continues there as its first group, " +
			"the branch and agent renamed to the group's lead, and returns once the daemon has the batch. " +
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
	case drivectl.ProposeStart:
		return runPropose(ctx, cmd, sa.Drive)
	case drivectl.FlowStart:
		// the daemon's own child passes --resume with --foreground
		if sa.Drive.Resume > 0 && !sa.Drive.Fore {
			// one-release alias: f10 start --resume <n>, typed by a person
			fmt.Fprintln(cmd.ErrWriter, drivectl.Renamed("f10 start --resume", "f10 resume <task-id>"))
		}

		return flowStart(ctx, cmd, argv, sa.Drive)
	case drivectl.MaybePickup:
		t, err := target.For(ctx, cmd)
		if err != nil {
			return err
		}

		pick, _, err := start.PickupFor(ctx, t, sa.Once.Token)
		if err != nil {
			return err
		}

		// a #n the host names a task is a flow start, closed check included
		if pick == nil {
			a, err := sa.Flow()
			if err != nil {
				return err
			}

			return flowStart(ctx, cmd, argv, a)
		}
	case drivectl.DiscussStart, drivectl.PickupStart:
	}

	return startOnce(ctx, cmd, onceRequest(sa.Once))
}

// flowStart runs a as a batch, or as a discussion when every task is closed.
func flowStart(ctx context.Context, cmd *cli.Command, argv []string, a drivectl.DriveArgs) error {
	if req := closedDiscussion(ctx, cmd, a); req != nil {
		return startOnce(ctx, cmd, req)
	}

	return Run(ctx, cmd, argv)
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
