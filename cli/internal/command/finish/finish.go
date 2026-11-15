// Package finish is the f10 finish and f10 janitor commands: it parses their
// flags and hands the work to the finish service.
package finish

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/agents"
	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/target"
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

	if err := agents.RequireAgents(); err != nil {
		return err
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	cwd, ws, err := finish.ProcessPlace()
	if err != nil {
		return err
	}

	return finish.Run(ctx, cmd.Writer, cmd.ErrWriter, t, finish.Request{
		Token: cmd.Args().First(),
		Close: cmd.String("close"),
		Flags: finish.Flags{
			Yes: cmd.Bool("yes"), Merge: cmd.Bool("merge"), LeaveOpen: cmd.Bool("local"), Keep: cmd.Bool("keep"),
			IgnoreChecks: cmd.Bool("ignore-checks"),
		},
		Cwd:       cwd,
		Workspace: ws,
	}, nil)
}

func JanitorCommand() *cli.Command {
	return &cli.Command{
		Name: "janitor",
		Usage: "remove task branches whose PR merged or closed, locally and on origin, and archive their plans; " +
			"f10 start and f10 finish run it every few hours",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Usage: "list what would be removed and change nothing"},
		},
		Action: runJanitor,
	}
}

func runJanitor(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	in := finish.JanitorFor(t)
	in.Dry = cmd.Bool("dry-run")

	if !in.Dry {
		if err := finish.TouchStamp(
			filepath.Join(t.Layout.StorageRoot.Path(), finish.JanitorStamp),
			time.Now(),
		); err != nil {
			return err
		}
	}

	lines, err := finish.Janitor(ctx, in)
	for _, l := range lines {
		fmt.Fprintln(cmd.Writer, l)
	}

	return err
}
