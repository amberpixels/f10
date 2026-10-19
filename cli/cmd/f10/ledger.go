package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ledger"
)

// The ledger noun reads the local record of what runs did, ~/.f10/f10.db,
// to check what a run left there. It opens the database read-only: a dump
// never migrates it or replays the spool.
func ledgerCommand() *cli.Command {
	return &cli.Command{
		Name:  "ledger",
		Usage: "read the local record of what f10 runs did",
		Commands: []*cli.Command{
			{
				Name:  "dump",
				Usage: "print the recorded events, oldest first",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "emit one JSON object per event"},
					&cli.StringFlag{Name: "project", Usage: "only this project id (`.` for the current directory's)"},
					&cli.StringFlag{Name: "task", Usage: "only this task id"},
					&cli.StringFlag{Name: "session", Usage: "only this session id"},
				},
				Action: runLedgerDump,
			},
		},
	}
}

func runLedgerDump(ctx context.Context, cmd *cli.Command) error {
	f := ledger.Filter{Project: cmd.String("project"), Task: cmd.String("task"), Session: cmd.String("session")}

	if f.Project == "." {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("ledger: %w", err)
		}

		f.Project = ledger.Where(ctx, cwd).Project
	}

	l, err := ledger.OpenRead(ctx, ledger.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil // nothing recorded yet
	}

	if err != nil {
		return err
	}

	defer l.Close()

	events, err := l.List(ctx, f)
	if err != nil {
		return err
	}

	for _, e := range events {
		if err := writeEvent(cmd, e); err != nil {
			return err
		}
	}

	return nil
}

func writeEvent(cmd *cli.Command, e ledger.Event) error {
	if cmd.Bool("json") {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("ledger: %w", err)
		}

		_, err = fmt.Fprintln(cmd.Writer, string(line))

		return err
	}

	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("ledger: %w", err)
	}

	_, err = fmt.Fprintf(cmd.Writer, "%s  %-8s  %-19s  %-8s  %-12s  %s\n",
		e.At.Local().Format(time.DateTime), e.Source, e.Kind, orDash(e.Task), orDash(e.Branch),
		strings.TrimSpace(string(payload)))

	return err
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}
