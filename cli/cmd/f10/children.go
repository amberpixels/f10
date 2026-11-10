package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktree"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// A parent's subtasks, as capture links them. Drive expands a parent into
// these, so the rows carry what choosing among them needs.

var errNoParentLink = errors.New("the tracker has no parent link")

// openSubtasks is the check start and drive make before a task runs as one
// of its own.
func openSubtasks(ctx context.Context, tr tracker, r ref.Ref) ([]string, error) {
	if tr.children == nil {
		return nil, nil
	}

	kids, err := tr.children(ctx, r)
	if errors.Is(err, errNoParentLink) {
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

// A childRow is one open subtask. Rows come in tree order: a child whose
// After: base is a sibling follows that sibling one level deeper.
type childRow struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	URL      string   `json:"url,omitempty"`
	State    string   `json:"state,omitempty"`
	Labels   []string `json:"labels"`
	After    string   `json:"after,omitempty"`
	Finished bool     `json:"finished"`
	Depth    int      `json:"depth"`

	number int
	suffix string // a launched start's worktree suffix: the checkout read is that one
	queued bool   // a drive's cap holds it back
	phase  string // where its drive's record says it stands, a drives.Phase name
}

type rawChild struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	State  string   `json:"state"`
	Labels []string `json:"labels"`
	After  string   `json:"after"`
	Body   string   `json:"body"`
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

	rows, err := trackerFor(t).children(ctx, r)
	if errors.Is(err, errNoParentLink) {
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
func openRows(rows []childRow) []childRow {
	return childTree(slices.DeleteFunc(slices.Clone(rows), func(r childRow) bool { return r.Finished }))
}

// rawChildren is the task's subtasks as the tracker holds them;
// errNoParentLink when it has no parent link to read.
func rawChildrenFor(ctx context.Context, t *target.Target, task ref.Ref) ([]rawChild, error) {
	return tasksFor(t).Children(ctx, task)
}

func shapeChildren(ctx context.Context, tr tracker, main string, raw []rawChild, keepClosed bool) ([]childRow, error) {
	rows := []childRow{}

	for _, c := range raw {
		closed := strings.EqualFold(c.State, "closed")
		if closed && !keepClosed {
			continue
		}

		r, err := tr.resolve(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("child %s: %w", c.ID, err)
		}

		row := childRow{
			ID:     r.ID,
			Title:  tr.titles.Strip(c.Title, r.ID, r.Number),
			URL:    c.URL,
			State:  c.State,
			Labels: c.Labels,
		}
		if row.Labels == nil {
			row.Labels = []string{}
		}

		row.number, _ = strconv.Atoi(r.Number)

		if line := cmp.Or(c.After, afterIn(c.Body)); line != "" {
			base, err := tr.resolve(ctx, line)
			if err != nil {
				return nil, fmt.Errorf("%s's body says After: %s: %w", r.ID, line, err)
			}

			row.After = base.ID
		}

		if closed {
			row.Finished = true
			rows = append(rows, row)

			continue
		}

		branched, err := hasTaskBranch(ctx, main, r.ID)
		if err != nil {
			return nil, err
		}

		row.Finished = finishedTask(taskInfo{}, tr.storage, r.ID, branched)
		rows = append(rows, row)
	}

	return childTree(rows), nil
}

// childTree orders rows under their After: base, siblings by number, else
// in the order given. Rows caught in a cycle come out at the top level, so
// the listing stays readable and drive refuses the cycle itself.
func childTree(rows []childRow) []childRow {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b childRow) int { return cmp.Compare(a.number, b.number) })

	ids := make(map[string]bool, len(sorted))
	for _, r := range sorted {
		ids[r.ID] = true
	}

	under := map[string][]childRow{}

	var roots []childRow

	for _, r := range sorted {
		if r.After != "" && r.After != r.ID && ids[r.After] {
			under[r.After] = append(under[r.After], r)
		} else {
			roots = append(roots, r)
		}
	}

	out := make([]childRow, 0, len(sorted))
	placed := map[string]bool{} // by id and suffix: two worktrees of a task are two rows

	var walk func(r childRow, depth int)
	walk = func(r childRow, depth int) {
		key := r.ID + "-" + strings.ToLower(r.suffix)
		if placed[key] {
			return
		}

		placed[key] = true
		r.Depth = depth
		out = append(out, r)

		for _, c := range under[r.ID] {
			walk(c, depth+1)
		}
	}

	for _, r := range roots {
		walk(r, 0)
	}

	for _, r := range sorted {
		walk(r, 0)
	}

	return out
}

// writeTree prints the rows as the tree drive shows, with no urls: a model
// reads it, and one needing a url runs f10 task open --link, as for status.
func writeTree(w io.Writer, rows []childRow) error {
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
func childTreeRows(rows []childRow) []treeRow {
	out := make([]treeRow, len(rows))
	for i, r := range rows {
		out[i] = treeRow{ID: r.ID, Title: r.Title, URL: r.URL, Depth: r.Depth}
	}

	return out
}

// relink puts child under parent, or with parent "" takes it out from under
// the parent it has.
func (h host) relink(ctx context.Context, parent, child string) error {
	if parent != "" {
		return h.LinkParent(ctx, parent, child)
	}

	return h.Unlink(ctx, child)
}
