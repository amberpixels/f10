package start

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	"github.com/amberpixels/f10/cli/internal/command"
)

// ForwardCommand is the forward verb, which hands a command to the agent already working on a task:
// a `/f10:ship` typed outside the task's worktree would run on the wrong
// branch, without the plan, so the skills call this first.
func ForwardCommand() *cli.Command {
	return &cli.Command{
		Name:      "forward",
		Hidden:    true,
		Category:  command.Plumbing,
		Usage:     "hand a command to the agent working on a task in another worktree",
		ArgsUsage: "<task-id>[-suffix] <text...>",
		Action:    runForward,
	}
}

func runForward(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()

	token := ""
	if len(args) > 0 {
		token = strings.TrimSpace(args[0])
	}

	if token == "" {
		return errors.New("forward needs a task id and the text to send")
	}

	text := strings.TrimSpace(strings.Join(args[1:], " "))
	if text == "" {
		return errors.New("forward needs the text to send after the task id")
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	caller, err := c.Caller(cmd)
	if err != nil {
		return err
	}

	res, err := c.Runs.Forward(ctx, connect.NewRequest(&f10v1.ForwardRequest{Task: token, Text: text, Caller: caller}))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	apiclient.Print(cmd.Writer, cmd.ErrWriter, res.Msg.GetPrinted())

	return nil
}
