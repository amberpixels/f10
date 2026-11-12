package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/tasktree"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A parent's subtasks, as capture links them. Drive expands a parent into
// these, so the rows carry what choosing among them needs.

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
	if errors.Is(err, tasksource.ErrNoParentLink) {
		return ui.Exit(ui.ExitDeclined, "%s: %v", r.ID, err)
	}

	if err != nil {
		return err
	}

	if cmd.Bool("open") {
		rows = OpenRows(rows)
	}

	plain := cmd.Bool("plain")
	if cmd.Bool("json") || (!plain && !ui.IsTerminal(cmd.Writer)) {
		return ui.JSON(cmd.Writer, rows)
	}

	return WriteTree(cmd.Writer, rows)
}

// OpenRows is rows without the finished ones, put in tree order again: a
// dependent of a finished base moves up to where its base stood.
func OpenRows(rows []stack.ChildRow) []stack.ChildRow {
	return stack.ChildTree(slices.DeleteFunc(slices.Clone(rows), func(r stack.ChildRow) bool { return r.Finished }))
}

// WriteTree prints the rows as the tree drive shows, with no urls: a model
// reads it, and one needing a url runs f10 task open --link, as for status.
func WriteTree(w io.Writer, rows []stack.ChildRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no children")

		return err
	}

	nodes := tasksource.SubtaskNodes(
		childTreeRows(rows),
		tasksource.TreeOpts{Extra: func(i int) ([]tasktree.Cell, []tasktree.Note) {
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
		}},
	)

	return tasktree.Tree{}.Write(w, nodes)
}

// childTreeRows is the subtasks as subtask tree rows.
func childTreeRows(rows []stack.ChildRow) []tasksource.TreeRow {
	out := make([]tasksource.TreeRow, len(rows))
	for i, r := range rows {
		out[i] = tasksource.TreeRow{ID: r.ID, Title: r.Title, URL: r.URL, Depth: r.Depth}
	}

	return out
}
