package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/tasktree"
)

// A parent's subtasks, as capture links them. Drive expands a parent into
// these, so the rows carry what choosing among them needs.

// exitNoParentLink is `task children`'s "the tracker has no parent link":
// a normal answer the drive skill reads as "drive the id as given".
const exitNoParentLink = 3

// exitParent is start's exit for a parent with unfinished subtasks: the
// start skill turns it into "drive them, or start one".
const exitParent = 6

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
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	rows, err := t.tracker().children(ctx, r)
	if errors.Is(err, errNoParentLink) {
		return cli.Exit(fmt.Sprintf("f10: %s: %v", r.ID, err), exitNoParentLink)
	}

	if err != nil {
		return err
	}

	if cmd.Bool("open") {
		rows = openRows(rows)
	}

	plain := cmd.Bool("plain")
	if cmd.Bool("json") || (!plain && !isTerminal(cmd.Writer)) {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(rows); err != nil {
			return fmt.Errorf("encoding rows: %w", err)
		}

		return nil
	}

	return writeTree(cmd.Writer, rows, plain)
}

// openRows is rows without the finished ones, put in tree order again: a
// dependent of a finished base moves up to where its base stood.
func openRows(rows []childRow) []childRow {
	return childTree(slices.DeleteFunc(slices.Clone(rows), func(r childRow) bool { return r.Finished }))
}

// rawChildren asks the driver's `children`, else the host; a driver that
// declines on a tracker no host CLI reaches has no parent link to read.
func (t *target) rawChildren(ctx context.Context, task ref.Ref) ([]rawChild, error) {
	if d := t.driver(); d != nil {
		out, err := d.Run(ctx, driver.VerbChildren, task.Number)
		if err == nil {
			var raw []rawChild
			if err := json.Unmarshal([]byte(out), &raw); err != nil {
				return nil, fmt.Errorf("parsing driver children rows: %w", err)
			}

			return raw, nil
		}

		if !errors.Is(err, driver.ErrUnsupported) {
			return nil, err
		}

		if !t.hostIssues() {
			return nil, errNoParentLink
		}
	}

	h, err := t.issues(driver.VerbChildren)
	if err != nil {
		return nil, err
	}

	if h.GLab() {
		return h.childItems(ctx, task.Number)
	}

	return h.subIssues(ctx, task.Number)
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

		row.Finished = finishedTask(taskInfo{}, tr.archive, r.ID, branched)
		rows = append(rows, row)
	}

	return childTree(rows), nil
}

// childTree orders rows under their After: base, siblings by number. Rows
// caught in a cycle come out at the top level, so the listing stays
// readable and drive refuses the cycle itself.
func childTree(rows []childRow) []childRow {
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, func(a, b childRow) int { return cmp.Compare(a.number, b.number) })

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
	placed := map[string]bool{}

	var walk func(r childRow, depth int)
	walk = func(r childRow, depth int) {
		if placed[r.ID] {
			return
		}

		placed[r.ID] = true
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

// writeTree prints the rows as the tree drive shows; plain adds each url on
// a line beneath, for text relayed where a link does not survive.
func writeTree(w io.Writer, rows []childRow, plain bool) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no children")

		return err
	}

	nodes := make([]tasktree.Node, len(rows))

	for i, r := range rows {
		cells := []tasktree.Cell{{Text: r.ID}, {Text: r.Title}}

		if r.After != "" {
			cells = append(cells, tasktree.Cell{Text: "after " + r.After})
		}

		if slices.Contains(r.Labels, "draft") {
			cells = append(cells, tasktree.Cell{Text: "draft"})
		}

		if r.Finished {
			cells = append(cells, tasktree.Cell{Text: "finished"})
		}

		node := tasktree.Node{Depth: r.Depth, Cells: cells}
		if plain && r.URL != "" {
			node.Notes = []tasktree.Note{{Label: "url", Text: r.URL, Whole: true}}
		}

		nodes[i] = node
	}

	return tasktree.Tree{}.Write(w, nodes)
}

// subIssues reads an issue's GitHub sub-issues. A 404 is a host without
// sub-issues: the issue itself was already read by the time drive asks.
func (h host) subIssues(ctx context.Context, number string) ([]rawChild, error) {
	type label struct {
		Name string `json:"name"`
	}

	found, err := forge.DecodePages[struct {
		Number  int     `json:"number"`
		Title   string  `json:"title"`
		State   string  `json:"state"`
		HTMLURL string  `json:"html_url"`
		Body    string  `json:"body"`
		Labels  []label `json:"labels"`
	}](ctx, h.Host, "repos/{owner}/{repo}/issues/"+number+"/sub_issues")
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil, errNoParentLink
		}

		return nil, err
	}

	raw := make([]rawChild, 0, len(found))

	for _, iss := range found {
		c := rawChild{
			ID:    strconv.Itoa(iss.Number),
			Title: iss.Title,
			URL:   iss.HTMLURL,
			State: iss.State,
			Body:  iss.Body,
		}

		for _, l := range iss.Labels {
			c.Labels = append(c.Labels, l.Name)
		}

		raw = append(raw, c)
	}

	return raw, nil
}

// childItemsQuery reads a GitLab issue's children from the work-item
// hierarchy, the only place GitLab keeps the parent link: REST issue links
// are "relates to" and "blocks", never a parent.
const childItemsQuery = `query($path: ID!, $iid: String!) {
  project(fullPath: $path) {
    workItems(iid: $iid) {
      nodes {
        widgets {
          ... on WorkItemWidgetHierarchy {
            children(first: 100) {
              nodes {
                iid title state webUrl description
                widgets { ... on WorkItemWidgetLabels { labels { nodes { title } } } }
              }
            }
          }
        }
      }
    }
  }
}`

// childItems reads a GitLab issue's work-item children; a GitLab without
// work items answers an unknown-field error, which is no parent link.
func (h host) childItems(ctx context.Context, number string) ([]rawChild, error) {
	var repo struct {
		Path string `json:"path_with_namespace"`
	}

	if err := h.Decode(ctx, &repo, "repo", "view", "-F", "json"); err != nil {
		return nil, err
	}

	type labels struct {
		Labels *struct {
			Nodes []struct {
				Title string `json:"title"`
			} `json:"nodes"`
		} `json:"labels"`
	}

	var payload struct {
		Data struct {
			Project struct {
				WorkItems struct {
					Nodes []struct {
						Widgets []struct {
							Children *struct {
								Nodes []struct {
									IID         string   `json:"iid"`
									Title       string   `json:"title"`
									State       string   `json:"state"`
									WebURL      string   `json:"webUrl"`
									Description string   `json:"description"`
									Widgets     []labels `json:"widgets"`
								} `json:"nodes"`
							} `json:"children"`
						} `json:"widgets"`
					} `json:"nodes"`
				} `json:"workItems"`
			} `json:"project"`
		} `json:"data"`
	}

	err := h.Decode(ctx, &payload, "api", "graphql", "-f", "query="+childItemsQuery,
		"-f", "path="+repo.Path, "-f", "iid="+number)
	if err != nil {
		if strings.Contains(err.Error(), "doesn't exist on type") {
			return nil, errNoParentLink
		}

		return nil, err
	}

	var raw []rawChild

	for _, item := range payload.Data.Project.WorkItems.Nodes {
		for _, w := range item.Widgets {
			if w.Children == nil {
				continue
			}

			for _, c := range w.Children.Nodes {
				child := rawChild{ID: c.IID, Title: c.Title, URL: c.WebURL, State: c.State, Body: c.Description}

				for _, lw := range c.Widgets {
					if lw.Labels == nil {
						continue
					}

					for _, l := range lw.Labels.Nodes {
						child.Labels = append(child.Labels, l.Title)
					}
				}

				raw = append(raw, child)
			}
		}
	}

	return raw, nil
}
