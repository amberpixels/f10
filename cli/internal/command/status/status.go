// Package status is the f10 status command: where a run stands, read from
// the daemon's API and printed here.
package status

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the status noun, which reads what the badge reads and says the rest: which step
// a run stopped on, why, and what unblocks it. A task's run is read from the
// checkout holding its branch, so a session can relay a driven agent's ask.
func Command() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "where this session's f10 run is, what it started or drove, a task's run in its own worktree, or every live run in this repo",
		ArgsUsage: "[task-id]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "every live run on this machine, not only this repo's"},
			ui.JSONFlag("emit the same runs as JSON"),
			&cli.BoolFlag{
				Name: "plain",
				Usage: "print a parent's or the launched-here tree as plain text, each url on a line of its own, " +
					"cut to $COLUMNS (else 100), for a skill to relay",
			},
		},
		Action: runStatus,
	}
}

func runStatus(ctx context.Context, cmd *cli.Command) error {
	all := cmd.Bool("all")
	token := strings.TrimSpace(cmd.Args().First())

	if token != "" && all {
		return errors.New("status takes a task id or --all, not both: the id already picks a checkout")
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	caller, err := c.Caller(cmd)
	if err != nil {
		return err
	}

	res, err := c.Runs.GetRunStatus(ctx, connect.NewRequest(&f10v1.GetRunStatusRequest{
		Task: token, All: all, Caller: caller,
	}))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	apiclient.Print(cmd.Writer, cmd.ErrWriter, res.Msg.GetPrinted())

	var rep statusview.Report
	if err := apiclient.View(res.Msg.GetView(), &rep); err != nil {
		return err
	}

	return statusview.Render(
		statusview.StatusView{Out: cmd.Writer, Warn: cmd.ErrWriter, JSON: cmd.Bool("json"), Plain: cmd.Bool("plain")},
		rep,
	)
}
