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
	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	configcmd "github.com/amberpixels/f10/cli/internal/command/config"
	"github.com/amberpixels/f10/cli/internal/drive"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/start"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/worktree"
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

	once, err := oneShot(ctx, cmd, argv)
	if err != nil {
		return err
	}

	if once == nil {
		once = closedDiscussion(ctx, cmd, argv)
	}

	if once == nil {
		return Run(ctx, cmd, argv)
	}

	return startOnce(ctx, cmd, once)
}

// closedDiscussion is the discussion a flow start becomes when every task
// it names is closed, nil otherwise. A resume, a range or an answer goes on
// with a drive already agreed, so it is never checked.
func closedDiscussion(ctx context.Context, cmd *cli.Command, argv []string) *f10v1.StartRequest {
	a, err := drivectl.ParseDriveArgs(argv)
	if err != nil || a.Plan != nil || a.Resume > 0 || a.Span || len(a.Answers) > 0 || len(a.Tasks) == 0 {
		return nil
	}

	// outside Herdr the drive refuses before anything is read
	if agents.RequireAgents() != nil {
		return nil
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return nil
	}

	labels := start.ClosedStart(ctx, t, a.Tasks)
	if labels == nil {
		return nil
	}

	fmt.Fprintln(cmd.ErrWriter, start.SwitchNote(labels, droppedDriveFlags(a)))

	return discussionRequest(a)
}

// discussionRequest is the flow start a as a discussion: its ids, base and
// agent kept, and none of the flow's flags, which --discuss refuses.
func discussionRequest(a drivectl.DriveArgs) *f10v1.StartRequest {
	return &f10v1.StartRequest{
		Mode:  f10v1.StartMode_START_MODE_DISCUSS,
		Token: a.Tasks[0],
		Tasks: a.Tasks[1:],
		After: a.After,
		Base:  a.Base,
		Agent: a.Agent,
	}
}

// droppedDriveFlags names the flags a discussion takes no use of.
func droppedDriveFlags(a drivectl.DriveArgs) []string {
	var out []string

	if a.PlanOnly {
		out = append(out, "--plan")
	}

	if a.Flow != "" {
		out = append(out, "--flow "+a.Flow)
	}

	if a.Pace != "" {
		out = append(out, "--pace "+a.Pace)
	}

	return out
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

	req.Token, words = words[0], words[1:]

	// the API resolves each id against the project, so a shape is enough here
	if req.GetMode() == f10v1.StartMode_START_MODE_DISCUSS {
		n := listed(words)
		req.Tasks, words = words[:n], words[n:]
	}

	req.Note = strings.Join(words, " ")

	return req, nil
}

// listed counts the leading words shaped as task ids: a discussion's other
// tasks, before its note.
func listed(words []string) int {
	for i, w := range words {
		if !ref.IsID("", w) {
			return i
		}
	}

	return len(words)
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
			if name == "--" {
				return nil, nil, fmt.Errorf("a range: %w; --discuss takes the ids one by one", errDriveFlag)
			}

			if strings.HasPrefix(name, "-") {
				return nil, nil, fmt.Errorf("%s: %w; --discuss and a PR pickup take ids and a note", name, errDriveFlag)
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
