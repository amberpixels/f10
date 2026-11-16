// Package finish is the f10 finish and f10 janitor commands: finish calls
// the daemon's API, and janitor sweeps this checkout in process.
package finish

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/urfave/cli/v3"

	agentsv1 "github.com/amberpixels/f10/cli/gen/agents/v1"
	f10v1 "github.com/amberpixels/f10/cli/gen/f10/v1"
	"github.com/amberpixels/f10/cli/internal/apiclient"
)

// Command is the finish verb, start's inverse. Nothing local moves until the merge
// commit is in the local default branch, and the plan leaves the worktree
// before the worktree goes: by hand, those are the steps that get skipped.
func Command() *cli.Command {
	return &cli.Command{
		Name: "finish",
		Usage: "finish a task: merge its PR (or a branch never pushed, here), archive the plan, remove the worktree, " +
			"close the workspace, pull main; a spike with no PR closes without a merge",
		ArgsUsage: "[<task-id>[-suffix] | <any id of a bundle>]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "yes",
				Usage: "when run from inside the task's own workspace, close it after the report instead of refusing",
			},
			&cli.BoolFlag{Name: "merge", Usage: "merge the PR even when someone else opened it"},
			&cli.BoolFlag{
				Name:  "empty",
				Usage: "finish a branch never pushed that adds nothing to the default branch, instead of refusing",
			},
			&cli.BoolFlag{
				Name:  "ignore-checks",
				Usage: "merge even when the PR head's checks failed or are still running",
			},
			&cli.BoolFlag{
				Name:  "local",
				Usage: "clean up the worktree, branch and workspace and leave the PR open: a review handed back",
			},
			&cli.BoolFlag{
				Name:  "keep",
				Usage: "for a spike, commit the experiment onto a kept/ branch instead of discarding it",
			},
			&cli.StringFlag{
				Name: "close",
				Usage: "for a bundle, the riders the PR delivers, closed with the lead: `IDS` (comma-separated), " +
					"all or none; the rest stay open",
			},
		},
		Action: runFinish,
	}
}

func runFinish(ctx context.Context, cmd *cli.Command) error {
	if cmd.Bool("merge") && cmd.Bool("local") {
		return errors.New("--merge and --local are exclusive: one merges the PR, the other leaves it open")
	}

	if cmd.Bool("keep") && (cmd.Bool("merge") || cmd.Bool("local")) {
		return errors.New("--keep keeps a spike's experiment, which has no PR to --merge or leave --local")
	}

	c, err := apiclient.Dial(ctx)
	if err != nil {
		return err
	}

	caller, err := c.Caller(cmd)
	if err != nil {
		return err
	}

	merge := f10v1.MergeMode_MERGE_MODE_PR

	switch {
	case cmd.Bool("merge"):
		merge = f10v1.MergeMode_MERGE_MODE_FORCE
	case cmd.Bool("local"):
		merge = f10v1.MergeMode_MERGE_MODE_CLOSE
	}

	res, err := c.Runs.Finish(ctx, connect.NewRequest(&f10v1.FinishRequest{
		Task:         cmd.Args().First(),
		Merge:        merge,
		IgnoreChecks: cmd.Bool("ignore-checks"),
		Keep:         cmd.Bool("keep"),
		Yes:          cmd.Bool("yes"),
		Empty:        cmd.Bool("empty"),
		Close:        cmd.String("close"),
		Caller:       caller,
	}))
	if err != nil {
		return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
	}

	m := res.Msg
	apiclient.Print(cmd.Writer, cmd.ErrWriter, m.GetPrinted())

	// the daemon leaves this workspace open: closing it ends this process,
	// so the close comes last, after the report
	if m.GetCloseWorkspaceId() == "" {
		return nil
	}

	_, err = c.Agents.CloseWorkspace(ctx, connect.NewRequest(&agentsv1.CloseWorkspaceRequest{
		Id: m.GetCloseWorkspaceId(), FocusId: m.GetFocusWorkspaceId(), Caller: c.AgentsCaller(cmd),
	}))

	return apiclient.Err(err, cmd.Writer, cmd.ErrWriter)
}
