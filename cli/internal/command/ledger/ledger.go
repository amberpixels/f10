// Package ledger is the f10 ledger command, which prints the recorded events.
package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// Command is the ledger noun, which reads the local record of what runs did, ~/.f10/f10.db,
// to check what a run left there. It replays the spool first, so spooled
// rows show, then reads the database read-only.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "ledger",
		Usage: "read the local record of what f10 runs did",
		Commands: []*cli.Command{
			{
				Name:  "dump",
				Usage: "print the recorded events, oldest first",
				Flags: []cli.Flag{
					ui.JSONFlag("emit one JSON object per event"),
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
	// the task is matched here rather than in SQL: a bundle's events carry
	// its lead, and a rider is found by the branch that names it
	task := strings.TrimSpace(cmd.String("task"))
	f := ledger.Filter{Project: cmd.String("project"), Session: cmd.String("session")}

	if f.Project == "." {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("ledger: %w", err)
		}

		f.Project = ledger.Where(ctx, cwd).Project
	}

	events, err := ledger.ReadEvents(ctx, ledger.Path(), f)
	if err != nil {
		return err
	}

	for _, e := range events {
		if task != "" && !e.OnTask(task) {
			continue
		}

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
		e.At.Local().Format(time.DateTime), e.Source, e.Kind, textx.OrDash(EventTask(e)), textx.OrDash(e.Branch),
		strings.TrimSpace(string(payload)))

	return err
}

// EventTask is the task column: a bundle's ids joined, else the task.
func EventTask(e ledger.Event) string {
	if members := e.Bundle(); members != nil {
		return strings.Join(members, ref.Separator)
	}

	return e.Task
}
