package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktree"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A parent's subtasks, as capture links them. Drive expands a parent into
// these, so the rows carry what choosing among them needs.

// openSubtasks is the check start and drive make before a task runs as one
// of its own.
func openSubtasks(ctx context.Context, tr stack.Tracker, r ref.Ref) ([]string, error) {
	if tr.Children == nil {
		return nil, nil
	}

	kids, err := tr.Children(ctx, r)
	if errors.Is(err, hostops.ErrNoParentLink) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("%s: reading its subtasks: %w", r.ID, err)
	}

	var open []string

	for _, k := range kids {
		if !k.Finished {
			open = append(open, k.ID)
		}
	}

	return open, nil
}

func runTaskChildren(ctx context.Context, cmd *cli.Command) error {
	t, err := target.For(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.Reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	rows, err := stack.TrackerFor(t).Children(ctx, r)
	if errors.Is(err, hostops.ErrNoParentLink) {
		return ui.Exit(ui.ExitDeclined, "%s: %v", r.ID, err)
	}

	if err != nil {
		return err
	}

	if cmd.Bool("open") {
		rows = openRows(rows)
	}

	plain := cmd.Bool("plain")
	if cmd.Bool("json") || (!plain && !ui.IsTerminal(cmd.Writer)) {
		return ui.JSON(cmd.Writer, rows)
	}

	return writeTree(cmd.Writer, rows)
}

// openRows is rows without the finished ones, put in tree order again: a
// dependent of a finished base moves up to where its base stood.
func openRows(rows []stack.ChildRow) []stack.ChildRow {
	return stack.ChildTree(slices.DeleteFunc(slices.Clone(rows), func(r stack.ChildRow) bool { return r.Finished }))
}

// writeTree prints the rows as the tree drive shows, with no urls: a model
// reads it, and one needing a url runs f10 task open --link, as for status.
func writeTree(w io.Writer, rows []stack.ChildRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no children")

		return err
	}

	nodes := subtaskNodes(childTreeRows(rows), treeOpts{extra: func(i int) ([]tasktree.Cell, []tasktree.Note) {
		r := rows[i]

		var cells []tasktree.Cell

		if r.After != "" {
			cells = append(cells, tasktree.Cell{Text: "after " + r.After})
		}

		if slices.Contains(r.Labels, "draft") {
			cells = append(cells, tasktree.Cell{Text: "draft"})
		}

		if r.Finished {
			cells = append(cells, tasktree.Cell{Text: "finished"})
		}

		return cells, nil
	}})

	return tasktree.Tree{}.Write(w, nodes)
}

// childTreeRows is the subtasks as subtask tree rows.
func childTreeRows(rows []stack.ChildRow) []treeRow {
	out := make([]treeRow, len(rows))
	for i, r := range rows {
		out[i] = treeRow{ID: r.ID, Title: r.Title, URL: r.URL, Depth: r.Depth}
	}

	return out
}
