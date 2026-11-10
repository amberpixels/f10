package main

import "github.com/amberpixels/f10/cli/internal/tasktree"

// A treeRow is one task as every subtask tree draws it: `task children`,
// `task report --children` and `status <parent>`.
type treeRow struct {
	ID, Title, URL string
	Depth          int
}

// treeOpts is how a subtask tree's rows become nodes.
type treeOpts struct {
	// head is a parent node above the rows, which hang one level under it
	head *treeRow
	// titleBelow puts the title on the line under the row, not in a cell
	titleBelow bool
	// url adds a `↗ <url>` cell after the title
	url bool
	// id replaces row i's id cell; nil prints the id as is
	id func(i int) tasktree.Cell
	// extra is row i's cells after the title and url, and the notes under it
	extra func(i int) ([]tasktree.Cell, []tasktree.Note)
}

// subtaskNodes is the one way task rows become tree nodes.
func subtaskNodes(rows []treeRow, o treeOpts) []tasktree.Node {
	nodes := make([]tasktree.Node, 0, len(rows)+1)
	depth := 0

	if o.head != nil {
		nodes = append(nodes, o.node(*o.head, 0, -1))
		depth = 1
	}

	for i, r := range rows {
		nodes = append(nodes, o.node(r, r.Depth+depth, i))
	}

	return nodes
}

// node is one row as a node; i < 0 is the head, which takes no hooks.
func (o treeOpts) node(r treeRow, depth, i int) tasktree.Node {
	n := tasktree.Node{Depth: depth, Cells: []tasktree.Cell{{Text: r.ID}}}
	if o.id != nil && i >= 0 {
		n.Cells[0] = o.id(i)
	}

	if o.titleBelow {
		n.Title = r.Title
	} else {
		n.Cells = append(n.Cells, tasktree.Cell{Text: r.Title})
	}

	if o.url && r.URL != "" {
		n.Cells = append(n.Cells, tasktree.Cell{Text: "↗ " + r.URL})
	}

	if o.extra != nil && i >= 0 {
		cells, notes := o.extra(i)
		n.Cells = append(n.Cells, cells...)
		n.Notes = notes
	}

	return n
}
