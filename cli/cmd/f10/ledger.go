package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/textx"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// The ledger noun reads the local record of what runs did, ~/.f10/f10.db,
// to check what a run left there. It replays the spool first, so spooled
// rows show, then reads the database read-only.
func ledgerCommand() *cli.Command {
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
		if task != "" && !onTask(e, task) {
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
		e.At.Local().Format(time.DateTime), e.Source, e.Kind, textx.OrDash(eventTask(e)), textx.OrDash(e.Branch),
		strings.TrimSpace(string(payload)))

	return err
}

// bundleOf is the bundle an event's branch ships when the event's task
// leads it, else nil.
func bundleOf(e ledger.Event) []string {
	i := strings.LastIndexByte(e.Task, '-')
	if i < 0 {
		return nil
	}

	members := ref.Members(e.Task[:i], e.Branch)
	if len(members) < 2 || !strings.EqualFold(members[0], e.Task) {
		return nil
	}

	return members
}

// onTask reports whether the event is the task's: its own, or its bundle's.
func onTask(e ledger.Event, id string) bool {
	if strings.EqualFold(e.Task, id) {
		return true
	}

	return slices.ContainsFunc(bundleOf(e), func(m string) bool { return strings.EqualFold(m, id) })
}

// eventTask is the task column: a bundle's ids joined, else the task.
func eventTask(e ledger.Event) string {
	if members := bundleOf(e); members != nil {
		return strings.Join(members, ref.Separator)
	}

	return e.Task
}
