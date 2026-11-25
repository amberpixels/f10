// Package status is the f10 status command: where a run stands, read from
// the daemon's API and printed here.
package status

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the status noun, which reads what the badge reads and says the rest: which step
// a run stopped on, why, and what unblocks it. A task's run is read from the
// checkout holding its branch, so a session can relay a driven agent's ask.
func Command() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "where this session's f10 run is, what it started, a task's run in its own worktree, or every live run in this repo",
		ArgsUsage: "[task-id]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "all",
				Usage: "every live run on this machine, not only this repo's; with --batches, every batch, not only the newest 20",
			},
			&cli.BoolFlag{
				Name:  "batches",
				Usage: "this project's batches, the newest 20, each running, stopped, done or cancelled",
			},
			&cli.BoolFlag{Name: "drives", Hidden: true, Usage: "the old spelling of --batches"},
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

	// one-release alias: f10 status --drives
	if cmd.Bool("drives") {
		fmt.Fprintln(cmd.ErrWriter, drivectl.Renamed("f10 status --drives", "f10 status --batches"))
	}

	if cmd.Bool("batches") || cmd.Bool("drives") {
		if token != "" {
			return errors.New("status --batches lists every batch: it takes no task id")
		}

		return listBatches(ctx, cmd, all)
	}

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

// listBatches is status --batches: the project's batches, as the daemon
// lists them.
func listBatches(ctx context.Context, cmd *cli.Command, all bool) error {
	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	caller, err := c.Caller(cmd)
	if err != nil {
		return err
	}

	res, err := c.Runs.ListBatches(ctx, connect.NewRequest(&f10v1.ListBatchesRequest{All: all, Caller: caller}))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	if !cmd.Bool("json") {
		apiclient.Print(cmd.Writer, cmd.ErrWriter, res.Msg.GetPrinted())

		return nil
	}

	out := make([]batchJSON, 0, len(res.Msg.GetBatches()))
	for _, d := range res.Msg.GetBatches() {
		row := batchJSON{Number: int(d.GetNumber()), Args: d.GetArgs(), State: d.GetState(), Tasks: d.GetTasks()}
		if d.GetStarted() != nil {
			row.Started = d.GetStarted().AsTime().Format(time.RFC3339)
		}

		out = append(out, row)
	}

	return ui.JSON(cmd.Writer, out)
}

// batchJSON is one batch as status --batches --json prints it.
type batchJSON struct {
	Number  int      `json:"number"`
	Args    []string `json:"args"`
	State   string   `json:"state"`
	Started string   `json:"started,omitempty"`
	// Tasks are the batch's tasks as typed. Any of them names the batch to
	// stop, resume and daemon logs, which take no number.
	Tasks []string `json:"tasks"`
}
