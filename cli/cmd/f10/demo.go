package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// The demo noun: no driver and no network behind it, like plan. A report is
// HTML, so it opens in the browser and there is no `read`.
func demoCommand() *cli.Command {
	return &cli.Command{
		Name:  "demo",
		Usage: "open the demo report saved for a task",
		Commands: []*cli.Command{
			{
				Name:      "open",
				Usage:     "open the demo report in the browser",
				ArgsUsage: "[task-id]",
				Flags:     []cli.Flag{linkFlag()},
				Action:    runDemoOpen,
			},
		},
	}
}

func runDemoOpen(ctx context.Context, cmd *cli.Command) error {
	t, path, err := demoReport(ctx, cmd)
	if err != nil {
		return err
	}

	if cmd.Bool("link") {
		_, err := fmt.Fprintln(cmd.Writer, path)

		return err
	}

	opener := shell.Opener()
	if opener == "" {
		return fmt.Errorf("no opener on PATH: use --link to get the path (%s)", path)
	}

	return shell.Passthrough(ctx, t.dir, opener, path)
}

// demoReport refuses a missing file rather than falling back to whatever
// else the directory holds, which is how a reader opens another task's demo.
func demoReport(ctx context.Context, cmd *cli.Command) (*target, string, error) {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return nil, "", err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return nil, "", err
	}

	path := filepath.Join(t.lay.StorageRoot, "demo", r.ID, "report.html")
	if _, err := os.Stat(path); err != nil {
		return nil, "", fmt.Errorf("no demo report for %s at %s", r.ID, path)
	}

	return t, path, nil
}
