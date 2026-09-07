package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/internal/shell"
)

// The plan noun: the one with no driver, no network and no host CLI behind
// it. A plan is a markdown file named after its task, so the same reference
// that reaches a task reaches the plan written for it.
func planCommand() *cli.Command {
	return &cli.Command{
		Name:  "plan",
		Usage: "read or open the plan saved for a task",
		Commands: []*cli.Command{
			{
				Name:      "read",
				Usage:     "write the plan to stdout",
				ArgsUsage: "[task-id]",
				Action:    runPlanRead,
			},
			{
				Name:      "open",
				Usage:     "open the plan in your editor",
				ArgsUsage: "[task-id]",
				Flags:     []cli.Flag{printFlag()},
				Action:    runPlanOpen,
			},
		},
	}
}

func runPlanRead(ctx context.Context, cmd *cli.Command) error {
	t, path, err := planFile(ctx, cmd)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	return writeDoc(ctx, cmd.Writer, t.dir, string(data))
}

func runPlanOpen(ctx context.Context, cmd *cli.Command) error {
	t, path, err := planFile(ctx, cmd)
	if err != nil {
		return err
	}

	if cmd.Bool("print") {
		_, err := fmt.Fprintln(cmd.Writer, path)

		return err
	}

	editor := shell.Editor()
	if len(editor) == 0 {
		return fmt.Errorf("no editor: set $EDITOR, or use --print to get the path (%s)", path)
	}

	return shell.Passthrough(ctx, t.dir, editor[0], append(editor[1:], path)...)
}

// planFile resolves the reference and names the plan file for it. A missing
// file is an error naming the path, because the alternative - falling back
// to some other plan - is how a run ends up reading the wrong task's work.
func planFile(ctx context.Context, cmd *cli.Command) (*target, string, error) {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return nil, "", err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return nil, "", err
	}

	path := filepath.Join(t.pb.PlansDir, r.ID+".md")
	if _, err := os.Stat(path); err != nil {
		return nil, "", fmt.Errorf("no plan for %s at %s", r.ID, path)
	}

	return t, path, nil
}
