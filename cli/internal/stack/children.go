package stack

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
)

// A ChildRow is one open subtask. Rows come in tree order: a child whose
// After: base is a sibling follows that sibling one level deeper.
type ChildRow struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	URL      string   `json:"url,omitempty"`
	State    string   `json:"state,omitempty"`
	Labels   []string `json:"labels"`
	After    string   `json:"after,omitempty"`
	Finished bool     `json:"finished"`
	Depth    int      `json:"depth"`

	Number int
	Suffix string // a launched start's worktree suffix: the checkout read is that one
	Queued bool   // a drive's cap holds it back
	Phase  string // where its drive's record says it stands, a drives.Phase name
}

// RawChildrenFor is the task's subtasks as the tracker holds them;
// errNoParentLink when it has no parent link to read.
func RawChildrenFor(ctx context.Context, t *target.Target, task ref.Ref) ([]hostops.RawChild, error) {
	return hostops.TasksFor(t).Children(ctx, task)
}

func ShapeChildren(
	ctx context.Context,
	tr Tracker,
	main string,
	raw []hostops.RawChild,
	keepClosed bool,
) ([]ChildRow, error) {
	rows := []ChildRow{}

	for _, c := range raw {
		closed := strings.EqualFold(c.State, "closed")
		if closed && !keepClosed {
			continue
		}

		r, err := tr.Resolve(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("child %s: %w", c.ID, err)
		}

		row := ChildRow{
			ID:     r.ID,
			Title:  tr.titles.Strip(c.Title, r.ID, r.Number),
			URL:    c.URL,
			State:  c.State,
			Labels: c.Labels,
		}
		if row.Labels == nil {
			row.Labels = []string{}
		}

		row.Number, _ = strconv.Atoi(r.Number)

		if line := cmp.Or(c.After, hostops.AfterIn(c.Body)); line != "" {
			base, err := tr.Resolve(ctx, line)
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

		branched, err := HasTaskBranch(ctx, main, r.ID)
		if err != nil {
			return nil, err
		}

		row.Finished = FinishedTask(hostops.TaskInfo{}, tr.Storage, r.ID, branched)
		rows = append(rows, row)
	}

	return ChildTree(rows), nil
}

// ChildTree orders rows under their After: base, siblings by number, else
// in the order given. Rows caught in a cycle come out at the top level, so
// the listing stays readable and drive refuses the cycle itself.
func ChildTree(rows []ChildRow) []ChildRow {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b ChildRow) int { return cmp.Compare(a.Number, b.Number) })

	ids := make(map[string]bool, len(sorted))
	for _, r := range sorted {
		ids[r.ID] = true
	}

	under := map[string][]ChildRow{}

	var roots []ChildRow

	for _, r := range sorted {
		if r.After != "" && r.After != r.ID && ids[r.After] {
			under[r.After] = append(under[r.After], r)
		} else {
			roots = append(roots, r)
		}
	}

	out := make([]ChildRow, 0, len(sorted))
	placed := map[string]bool{} // by id and suffix: two worktrees of a task are two rows

	var walk func(r ChildRow, depth int)
	walk = func(r ChildRow, depth int) {
		key := r.ID + "-" + strings.ToLower(r.Suffix)
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
