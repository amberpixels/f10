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
)

// A parent's subtasks, as capture links them: GitHub sub-issues, GitLab
// work-item children, or a driver's `children` verb. Drive expands a parent
// into these, so the rows carry what choosing among them needs: the After:
// base, the draft label, and whether each is finished the way drive
// decides it.

// exitNoParentLink is `task children`'s "the tracker has no parent link":
// a normal answer the drive skill reads as "drive the id as given".
const exitNoParentLink = 3

// exitParent is start's exit for a parent with unfinished subtasks: the
// start skill turns it into "drive them, or start one".
const exitParent = 6

// errNoParentLink is a tracker that cannot link a subtask to its parent.
var errNoParentLink = errors.New("the tracker has no parent link")

// openSubtasks is the ids of a task's unfinished subtasks, in tree order:
// the check start and drive make before a task runs as one of its own. A
// tracker with no parent link has none.
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
}

// A rawChild is a subtask as its source returns it, before f10 reads its
// body, its branch and its plan archive.
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

	if cmd.Bool("json") || !isTerminal(cmd.Writer) {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(rows); err != nil {
			return fmt.Errorf("encoding rows: %w", err)
		}

		return nil
	}

	return writeTree(cmd.Writer, rows)
}

// rawChildren asks the driver's `children`, else the host. A driver that
// declines the verb and a tracker no host CLI reaches both have no parent
// link to read.
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

	h, err := t.issues()
	if err != nil {
		return nil, err
	}

	if h.GLab() {
		return h.childItems(ctx, task.Number)
	}

	return h.subIssues(ctx, task.Number)
}

// shapeChildren drops closed rows, resolves each id and After: base to the
// project's format, marks the finished ones and puts them in tree order.
func shapeChildren(ctx context.Context, tr tracker, main string, raw []rawChild) ([]childRow, error) {
	rows := []childRow{}

	for _, c := range raw {
		if strings.EqualFold(c.State, "closed") {
			continue
		}

		r, err := tr.resolve(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("child %s: %w", c.ID, err)
		}

		row := childRow{ID: r.ID, Title: c.Title, URL: c.URL, State: c.State, Labels: c.Labels}
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

		branched, err := hasTaskBranch(ctx, main, r.ID)
		if err != nil {
			return nil, err
		}

		row.Finished = finishedTask(taskInfo{}, tr.archive, r.ID, branched)
		rows = append(rows, row)
	}

	return childTree(rows), nil
}

// childTree orders rows so each follows its After: base when that base is a
// sibling, one level deeper; siblings at one level go by number. Rows caught
// in a cycle come out at the top level, so the listing stays readable and
// drive refuses the cycle itself.
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

// writeTree prints the rows as the tree drive shows: box lines down each
// After: chain, the base named beside each dependent, and draft marked.
func writeTree(w io.Writer, rows []childRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "no children")

		return err
	}

	// last[i] reports whether rows[i] is the last of its siblings
	last := make([]bool, len(rows))

	for i, r := range rows {
		last[i] = true

		for _, next := range rows[i+1:] {
			if next.Depth < r.Depth {
				break
			}

			if next.Depth == r.Depth {
				last[i] = false

				break
			}
		}
	}

	var open []bool // per depth above this row: a sibling still follows

	for i, r := range rows {
		open = open[:min(len(open), r.Depth)]

		var b strings.Builder

		for d := 1; d < r.Depth; d++ {
			if d < len(open) && open[d] {
				b.WriteString("│  ")
			} else {
				b.WriteString("   ")
			}
		}

		switch {
		case r.Depth == 0:
		case last[i]:
			b.WriteString("└─ ")
		default:
			b.WriteString("├─ ")
		}

		fmt.Fprintf(&b, "%s  %s", r.ID, r.Title)

		if r.After != "" {
			b.WriteString("  after " + r.After)
		}

		if slices.Contains(r.Labels, "draft") {
			b.WriteString("  draft")
		}

		if r.Finished {
			b.WriteString("  finished")
		}

		if _, err := fmt.Fprintln(w, b.String()); err != nil {
			return fmt.Errorf("writing rows: %w", err)
		}

		for len(open) <= r.Depth {
			open = append(open, false)
		}

		open[r.Depth] = !last[i]
	}

	return nil
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

// childItems reads a GitLab issue's work-item children. A GitLab without
// work items answers the query with an unknown-field error, which is a
// tracker with no parent link rather than a failure.
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
