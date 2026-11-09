package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/tasktree"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// `task report` renders the facts block a skill's report opens with, so the
// rows and the tree under a parent are drawn the same every run.

func runTaskReport(ctx context.Context, cmd *cli.Command) error {
	tokens := cmd.Args().Slice()
	if len(tokens) == 0 {
		return errors.New("report needs a task id")
	}

	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	refs := make([]ref.Ref, len(tokens))

	for i, token := range tokens {
		if r, err := t.reference(ctx, token); err == nil {
			refs[i] = r
		}
	}

	known := t.taskRefs(ctx, refs, true, false, cmd.ErrWriter)

	if cmd.Bool("children") {
		return writeParentTree(ctx, cmd, t, tokens[0], refs[0], known[refs[0].ID])
	}

	if len(tokens) == 1 {
		return writeTaskFacts(cmd, tokens[0], refs[0].ID, known[refs[0].ID])
	}

	nodes := make([]tasktree.Node, len(tokens))
	for i, token := range tokens {
		nodes[i] = reportNode(token, refs[i].ID, known[refs[i].ID].URL)
	}

	return tasktree.Tree{Align: true}.Write(cmd.Writer, nodes)
}

func reportNode(token, id, url string) tasktree.Node {
	if id == "" || url == "" {
		return tasktree.Node{Cells: []tasktree.Cell{{Text: cmp.Or(id, token)}}}
	}

	return tasktree.Node{Cells: []tasktree.Cell{{Text: id + ":"}, {Text: "↗ " + url}}}
}

// writeParentTree hangs a parent's open subtasks under it, in After: order,
// each row with its title and url, so a split reads as one block. A parent
// the tracker cannot link prints as one task's facts.
func writeParentTree(ctx context.Context, cmd *cli.Command, t *target, token string, r ref.Ref,
	k ledger.TaskRef,
) error {
	if r.ID == "" {
		return writeTaskFacts(cmd, token, "", k)
	}

	kids, err := t.tracker().children(ctx, r)
	switch {
	case errors.Is(err, errNoParentLink):
		ui.Warnf(cmd.ErrWriter, "%s: %v: no subtasks to list", r.ID, err)
	case err != nil:
		return err
	}

	kids = openRows(kids)
	if len(kids) == 0 {
		return writeTaskFacts(cmd, token, r.ID, k)
	}

	nodes := []tasktree.Node{titledNode(r.ID, k.Title, k.URL, 0)}
	for _, c := range kids {
		nodes = append(nodes, titledNode(c.ID, c.Title, c.URL, c.Depth+1))
	}

	t.rememberRefs(ctx, childRefs(kids)...)

	return tasktree.Tree{Align: true}.Write(cmd.Writer, nodes)
}

func titledNode(id, title, url string, depth int) tasktree.Node {
	cells := []tasktree.Cell{{Text: id}, {Text: title}}
	if url != "" {
		cells = append(cells, tasktree.Cell{Text: "↗ " + url})
	}

	return tasktree.Node{Depth: depth, Cells: cells}
}

func writeTaskFacts(cmd *cli.Command, token, id string, k ledger.TaskRef) error {
	rows := [][3]string{{"task:", " ", cmp.Or(id, token)}}
	if k.Title != "" {
		rows = append(rows, [3]string{"title:", " ", k.Title})
	}

	if k.URL != "" {
		rows = append(rows, [3]string{"url:", "↗", k.URL})
	}

	for _, r := range rows {
		if _, err := fmt.Fprintf(cmd.Writer, "%-6s %s %s\n", r[0], r[1], r[2]); err != nil {
			return fmt.Errorf("writing the report: %w", err)
		}
	}

	return nil
}
