package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/internal/driver"
	"github.com/amberpixels/f10/internal/shell"
)

// The task noun goes through the project's driver when it has one, and
// through the host's issues when it does not - so a repo with no .f10/ at
// all still answers, and a repo whose tracker has no CLI answers the same
// way once it drops in a driver.
func taskCommand() *cli.Command {
	return &cli.Command{
		Name:  "task",
		Usage: "read, open or search the project's tasks",
		Commands: []*cli.Command{
			{
				Name:      "read",
				Usage:     "write the task to stdout as markdown",
				ArgsUsage: "[task-id]",
				Action:    runTaskRead,
			},
			{
				Name:      "open",
				Usage:     "open the task in the browser",
				ArgsUsage: "[task-id]",
				Flags:     []cli.Flag{printFlag()},
				Action:    runTaskOpen,
			},
			{
				Name:      "search",
				Usage:     "find tasks by description",
				ArgsUsage: "<query>",
				Action:    runTaskSearch,
			},
		},
	}
}

func runTaskRead(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	if d := driver.Find(t.res.StorageRoot, t.dir); d != nil {
		doc, err := d.Run(ctx, driver.VerbRead, r.Number)
		if err == nil {
			return writeDoc(ctx, cmd.Writer, t.dir, doc)
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return err
		}
	}

	doc, err := hostIssueDoc(ctx, t, r.ID, r.Number)
	if err != nil {
		return err
	}

	return writeDoc(ctx, cmd.Writer, t.dir, doc)
}

func runTaskOpen(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	if d := driver.Find(t.res.StorageRoot, t.dir); d != nil {
		url, err := d.Run(ctx, driver.VerbURL, r.Number)
		if err == nil {
			return follow(ctx, cmd.Writer, t.dir, strings.TrimSpace(url), cmd.Bool("print"))
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return err
		}
	}

	host, err := t.hostCLI()
	if err != nil {
		return err
	}

	if cmd.Bool("print") {
		url, err := hostIssueURL(ctx, t, host, r.Number)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(cmd.Writer, url)

		return err
	}

	return shell.Passthrough(ctx, t.dir, host, "issue", "view", r.Number, "--web")
}

func runTaskSearch(ctx context.Context, cmd *cli.Command) error {
	query := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
	if query == "" {
		return errors.New("search needs a query")
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	rows, err := searchRows(ctx, t, query)
	if err != nil {
		return err
	}

	return writeRows(cmd.Writer, rows)
}

// A row is what a search returns: the four fields f10 relies on. A driver
// may carry more, and f10 neither requires nor reads them.
type row struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
	URL    string `json:"url,omitempty"`
}

func searchRows(ctx context.Context, t *target, query string) ([]row, error) {
	if d := driver.Find(t.res.StorageRoot, t.dir); d != nil {
		out, err := d.Run(ctx, driver.VerbSearch, query)
		if err == nil {
			var rows []row
			if err := json.Unmarshal([]byte(out), &rows); err != nil {
				return nil, fmt.Errorf("parsing driver search rows: %w", err)
			}

			return rows, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return nil, err
		}
	}

	return hostIssueSearch(ctx, t, query)
}

// writeRows applies the same rule reading a task does: a terminal gets a
// table, anything else gets the JSON. f10 prints rows and never picks a
// winner among them - choosing is fzf's job in a shell and the agent's in a
// turn, and a picker here would be wrong for one of the two.
func writeRows(w io.Writer, rows []row) error {
	if !isTerminal(w) {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if err := enc.Encode(rows); err != nil {
			return fmt.Errorf("encoding rows: %w", err)
		}

		return nil
	}

	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no matches")

		return err
	}

	idW, statusW := 0, 0
	for _, r := range rows {
		idW = max(idW, len(r.ID))
		statusW = max(statusW, len(r.Status))
	}

	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%-*s  %-*s  %s\n", idW, r.ID, statusW, r.Status, r.Title); err != nil {
			return fmt.Errorf("writing rows: %w", err)
		}
	}

	return nil
}
