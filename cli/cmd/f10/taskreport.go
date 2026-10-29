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
)

// `task report` renders the facts block a skill's report opens with, so the
// rows and the tree under a parent are drawn the same every run.

func runTaskReport(ctx context.Context, cmd *cli.Command) error {
	tokens := cmd.Args().Slice()
	if len(tokens) == 0 {
		return errors.New("report needs a task id")
	}

	children := cmd.Bool("children")
	if children && len(tokens) > 1 {
		return errors.New("report --children takes one parent id")
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

	var nodes []tasktree.Node

	if children {
		if refs[0].ID == "" {
			return writeTaskFacts(cmd, tokens[0], "", ledger.TaskRef{})
		}

		kids, err := t.tracker().children(ctx, refs[0])
		switch {
		case errors.Is(err, errNoParentLink):
			fmt.Fprintf(cmd.ErrWriter, "f10: %s: %v: no subtasks to list\n", refs[0].ID, err)
		case err != nil:
			return err
		}

		if len(kids) == 0 {
			return writeTaskFacts(cmd, tokens[0], refs[0].ID, known[refs[0].ID])
		}

		nodes = append(nodes, reportNode(tokens[0], refs[0].ID, known[refs[0].ID].URL, 0))

		for _, k := range kids {
			nodes = append(nodes, reportNode(k.ID, k.ID, k.URL, k.Depth+1))
		}

		t.rememberRefs(ctx, childRefs(kids)...)
	} else {
		if len(tokens) == 1 {
			return writeTaskFacts(cmd, tokens[0], refs[0].ID, known[refs[0].ID])
		}

		for i, token := range tokens {
			nodes = append(nodes, reportNode(token, refs[i].ID, known[refs[i].ID].URL, 0))
		}
	}

	return tasktree.Tree{Align: true}.Write(cmd.Writer, nodes)
}

func reportNode(token, id, url string, depth int) tasktree.Node {
	if id == "" || url == "" {
		return tasktree.Node{Depth: depth, Cells: []tasktree.Cell{{Text: cmp.Or(id, token)}}}
	}

	return tasktree.Node{Depth: depth, Cells: []tasktree.Cell{{Text: id + ":"}, {Text: "↗ " + url}}}
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
