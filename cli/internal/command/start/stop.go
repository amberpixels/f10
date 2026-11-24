package start

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	"github.com/amberpixels/f10/cli/internal/drivectl"
)

// StopCommand is the stop verb: it stops a batch from prompting further and
// closes its asks. A task id stops the batch holding it, whole, since a
// batch's tasks wait on one another.
func StopCommand() *cli.Command {
	return &cli.Command{
		Name:      "stop",
		Usage:     "stop the batch a task belongs to: no agent is prompted further, and f10 resume <task-id> goes on with it",
		ArgsUsage: "<task-id>",
		Action:    runStop,
	}
}

func runStop(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return errors.New("stop takes one task id, any task of the batch: f10 stop GH-12")
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	caller, err := c.Caller(cmd)
	if err != nil {
		return err
	}

	token := strings.TrimSpace(cmd.Args().First())

	var printed *f10v1.Printed

	if n, ok := drivectl.OldNumber(token); ok {
		fmt.Fprintln(cmd.ErrWriter, drivectl.Renamed("a batch number", "f10 stop <task-id>"))

		//nolint:staticcheck // the old spelling, by number, for one release
		res, err := c.Runs.CancelDrive(ctx, connect.NewRequest(&f10v1.CancelDriveRequest{
			Drive: int32(n), Caller: caller, //nolint:gosec // a batch number
		}))
		if err != nil {
			return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
		}

		printed = res.Msg.GetPrinted()
	} else {
		res, err := c.Runs.StopBatch(ctx, connect.NewRequest(&f10v1.StopBatchRequest{Task: token, Caller: caller}))
		if err != nil {
			return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
		}

		printed = res.Msg.GetPrinted()
	}

	apiclient.Print(cmd.Writer, cmd.ErrWriter, printed)

	return nil
}
