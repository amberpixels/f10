package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/shell"
)

// The demo noun: the second one behind no driver and no network, and the
// first whose file is a page. A report is named after its task exactly as a
// plan is, so the same reference reaches both - but it is HTML, so it goes
// to the browser rather than the editor, and `read` stays absent rather
// than being redefined to mean "cat some markup at a terminal".
func demoCommand() *cli.Command {
	return &cli.Command{
		Name:  "demo",
		Usage: "open the demo report saved for a task",
		Commands: []*cli.Command{
			{
				Name:      "open",
				Usage:     "open the demo report in the browser",
				ArgsUsage: "[task-id]",
				Flags:     []cli.Flag{printFlag()},
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

	if cmd.Bool("print") {
		_, err := fmt.Fprintln(cmd.Writer, path)

		return err
	}

	opener := shell.Opener()
	if opener == "" {
		return fmt.Errorf("no opener on PATH: use --print to get the path (%s)", path)
	}

	return shell.Passthrough(ctx, t.dir, opener, path)
}

// demoReport resolves the reference and names the report written for it. A
// missing file is an error naming the path, for the reason planFile gives:
// falling back to whatever else the directory holds is how a reader ends up
// reading another task's demo.
func demoReport(ctx context.Context, cmd *cli.Command) (*target, string, error) {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return nil, "", err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return nil, "", err
	}

	path := filepath.Join(t.res.StorageRoot, "demo", r.ID, "report.html")
	if _, err := os.Stat(path); err != nil {
		return nil, "", fmt.Errorf("no demo report for %s at %s", r.ID, path)
	}

	return t, path, nil
}
