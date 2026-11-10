package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/state"
	"github.com/amberpixels/f10/cli/internal/statusview"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The status noun reads what the badge reads and says the rest: which step
// a run stopped on, why, and what unblocks it. A task's run is read from the
// checkout holding its branch, so a session can relay a driven agent's ask.
func statusCommand() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "where this session's f10 run is, what it started or drove, a task's run in its own worktree, or every live run in this repo",
		ArgsUsage: "[task-id]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "every live run on this machine, not only this repo's"},
			ui.JSONFlag("emit the same runs as JSON"),
			&cli.BoolFlag{
				Name: "plain",
				Usage: "print a parent's, a finished task's or the launched-here tree as plain text, without urls, " +
					"cut to $COLUMNS (else 100), for a skill to relay",
			},
		},
		Action: runStatus,
	}
}

func runStatus(ctx context.Context, cmd *cli.Command) error {
	all := cmd.Bool("all")
	token := strings.TrimSpace(cmd.Args().First())
	v := statusview.StatusView{Out: cmd.Writer, Warn: cmd.ErrWriter, JSON: cmd.Bool("json"), Plain: cmd.Bool("plain")}

	if token == "" {
		dir, scoped, err := target.RootDir(cmd)
		if err != nil {
			return err
		}

		_, err = statusview.BareStatus(ctx, v, dir, scoped, state.Session(), all)

		return err
	}

	if all {
		return errors.New("status takes a task id or --all, not both: the id already picks a checkout")
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	rd, err := statusview.NewStatusReader(t, time.Now())
	if err != nil {
		return err
	}

	_, err = rd.Task(ctx, v, token, false)

	return err
}
