package finish

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/finish"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
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

	if err := worktree.RequireAgents(); err != nil {
		return err
	}

	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	h, err := hostops.HostOf(t)
	if err != nil {
		return err
	}

	r, suffix, branch, err := finish.Ref(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	if suffix != "" && r.Bundle != nil {
		return fmt.Errorf("%s ships in bundle %s, which takes no suffix", r.ID, r.Label())
	}

	// any member finishes the whole bundle, found by its lead
	r = r.Head()

	var closing []string

	if v := strings.TrimSpace(cmd.String("close")); v != "" {
		if len(r.Riders()) == 0 {
			return fmt.Errorf("--close names riders, and %s ships alone", r.ID)
		}

		resolve := func(tok string) (ref.Ref, error) { return t.Reference(ctx, tok) }
		if closing, err = finish.CloseChoice(r, v, resolve); err != nil {
			return err
		}
	}

	// drive's own finish runs under the lease drive holds, so only the
	// command takes it
	lease, err := finish.Lease(t.Layout.StorageRoot.Path(), r.ID)
	if err != nil {
		return err
	}
	defer lease.Release()

	in := finish.InputFor(t, cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot), h, r)
	if in.Cwd, in.Workspace, err = finish.ProcessPlace(); err != nil {
		return err
	}

	in.Suffix, in.Branch = suffix, branch
	in.Flags = finish.Flags{
		Yes: cmd.Bool("yes"), Merge: cmd.Bool("merge"), LeaveOpen: cmd.Bool("local"), Keep: cmd.Bool("keep"),
	}
	in.Close = closing
	in.Sweep = func(ctx context.Context) {
		finish.SweepIfDue(
			ctx,
			cmd.ErrWriter,
			filepath.Join(t.Layout.StorageRoot.Path(), finish.JanitorStamp),
			time.Now(),
			func(ctx context.Context) ([]string, error) { return finish.Janitor(ctx, finish.JanitorFor(t)) },
		)
	}

	err = finish.Finish(ctx, cmd.Writer, in)

	if fp := (finish.ForeignPullError{}); errors.As(err, &fp) {
		return ui.Exit(ui.ExitDeclined, "%v", fp)
	}

	if re := (finish.RidersError{}); errors.As(err, &re) {
		return ui.Exit(ui.ExitRiders, "%v", re)
	}

	return err
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
