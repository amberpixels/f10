package hostops

import "github.com/amberpixels/f10/cli/internal/tasktree"

// A TreeRow is one task as every subtask tree draws it: `task children`,
// `task report --children` and `status <parent>`.
type TreeRow struct {
	ID, Title, URL string
	Depth          int
}

// TreeOpts is how a subtask tree's rows become nodes.
type TreeOpts struct {
	// Head is a parent node above the rows, which hang one level under it
	Head *TreeRow
	// TitleBelow puts the title on the line under the row, not in a cell
	TitleBelow bool
	// Url adds a `↗ <url>` cell after the title
	URL bool
	// Id replaces row i's id cell; nil prints the id as is
	ID func(i int) tasktree.Cell
	// Extra is row i's cells after the title and url, and the notes under it
	Extra func(i int) ([]tasktree.Cell, []tasktree.Note)
}

// node is one row as a node; i < 0 is the head, which takes no hooks.
func (o TreeOpts) node(r TreeRow, depth, i int) tasktree.Node {
	n := tasktree.Node{Depth: depth, Cells: []tasktree.Cell{{Text: r.ID}}}
	if o.ID != nil && i >= 0 {
		n.Cells[0] = o.ID(i)
	}

	if o.TitleBelow {
		n.Title = r.Title
	} else {
		n.Cells = append(n.Cells, tasktree.Cell{Text: r.Title})
	}

	if o.URL && r.URL != "" {
		n.Cells = append(n.Cells, tasktree.Cell{Text: "↗ " + r.URL})
	}

	if o.Extra != nil && i >= 0 {
		cells, notes := o.Extra(i)
		n.Cells = append(n.Cells, cells...)
		n.Notes = notes
	}

	return n
}

// SubtaskNodes is the one way task rows become tree nodes.
func SubtaskNodes(rows []TreeRow, o TreeOpts) []tasktree.Node {
	nodes := make([]tasktree.Node, 0, len(rows)+1)
	depth := 0

	if o.Head != nil {
		nodes = append(nodes, o.node(*o.Head, 0, -1))
		depth = 1
	}

	for i, r := range rows {
		nodes = append(nodes, o.node(r, r.Depth+depth, i))
	}

	return nodes
}
