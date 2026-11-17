package start

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
)

// StopCommand is the stop verb: it stops a drive from prompting further and
// closes its asks. A task id stops the drive holding it, whole, since a
// drive's tasks wait on one another.
func StopCommand() *cli.Command {
	return &cli.Command{
		Name:      "stop",
		Usage:     "stop a drive f10 start runs, by its number or a task it holds; f10 start --resume N goes on with it",
		ArgsUsage: "<task-id | drive number>",
		Action:    runStop,
	}
}

func runStop(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return errors.New("stop takes one task id or drive number: f10 stop 3")
	}

	req := &f10v1.CancelDriveRequest{}

	token := strings.TrimSpace(cmd.Args().First())
	if n, err := strconv.Atoi(token); err == nil && n > 0 {
		req.Drive = int32(n) //nolint:gosec // a drive number
	} else {
		req.Task = token
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	if req.Caller, err = c.Caller(cmd); err != nil {
		return err
	}

	res, err := c.Runs.CancelDrive(ctx, connect.NewRequest(req))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	apiclient.Print(cmd.Writer, cmd.ErrWriter, res.Msg.GetPrinted())

	return nil
}
