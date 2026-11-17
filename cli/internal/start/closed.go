package start

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/lore"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/regression"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
)

const (
	shippedShown = 10  // shipping commits a prompt lists before "and N more"
	openListed   = 200 // open tasks read for an id one digit off
)

// A closedCheck tells a closed task from one a start may run. A task with
// a branch is a run being resumed, such as a drive finishing a PR whose
// merge closed the issue, so it never counts as closed here.
type closedCheck struct {
	tr       stack.Tracker
	branched func(ctx context.Context, id string) bool
}

func closedCheckFor(t *target.Target) closedCheck {
	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)

	return closedCheck{
		tr: stack.TrackerFor(t),
		branched: func(ctx context.Context, id string) bool {
			has, err := stack.HasTaskBranch(ctx, main, id)

			return err != nil || has
		},
	}
}

// ClosedStart is the labels of the tasks ids names when every one of them
// is closed and none has a branch, nil otherwise: such a start has nothing
// to run, so it opens a discussion instead. A bundle with a closed member
// switches the start whatever else it lists, because the drive judges a
// bundle by its lead and would ship that member again. A read that fails
// counts as open, so the check never blocks a start.
func ClosedStart(ctx context.Context, t *target.Target, ids []string) []string {
	return closedCheckFor(t).all(ctx, ids)
}

func (c closedCheck) all(ctx context.Context, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}

	var (
		labels  []string
		open    bool
		bundled bool
	)

	for _, token := range ids {
		id, _ := ref.SplitSuffix(strings.TrimSpace(token))

		r, err := c.tr.Resolve(ctx, id)
		if err != nil {
			open = true

			continue
		}

		r = r.Head()
		if !c.closed(ctx, r) {
			open = true

			continue
		}

		labels = append(labels, r.Label())
		bundled = bundled || len(r.Riders()) > 0
	}

	if len(labels) == 0 || open && !bundled {
		return nil
	}

	return labels
}

// closed: a bundle counts as closed when any member is, since starting it
// would ship that member again. A driver that states no state is open.
func (c closedCheck) closed(ctx context.Context, r ref.Ref) bool {
	if c.branched(ctx, r.ID) {
		return false
	}

	for _, m := range r.MemberRefs() {
		if info, err := c.tr.Info(ctx, m); err == nil && info.Stated && info.Closed {
			return true
		}
	}

	return false
}

// SwitchNote is the line a start that became a discussion prints, naming
// the flags a discussion has no use for.
func SwitchNote(labels, dropped []string) string {
	n := fmt.Sprintf("%s %s closed: opening a discussion instead of running the flow",
		strings.Join(labels, ", "), map[bool]string{true: "is", false: "are"}[len(labels) == 1])

	if len(dropped) > 0 {
		n += " (" + strings.Join(dropped, ", ") + " dropped)"
	}

	return n
}

// describeClosed reads, for each discussed task that is closed, what
// shipped it and the open tasks a typo would have meant.
func describeClosed(ctx context.Context, t *target.Target, labels []string) []launch.Closed {
	c := closedCheckFor(t)
	main := cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)
	prefix := t.Effective.IDPrefix

	var (
		out  []launch.Closed
		open []hostops.Row
		read bool
	)

	for _, label := range labels {
		r, err := c.tr.Resolve(ctx, label)
		if err != nil || !c.closed(ctx, r.Head()) {
			continue
		}

		r = r.Head()

		if !read {
			open, read = openTasks(ctx, t), true
		}

		d := shipped(ctx, main, prefix, shippingIDs(ctx, c.tr, r))
		d.ID = r.Label()

		for _, row := range open {
			if oneDigitOff(r.ID, row.ID) {
				d.Typos = append(d.Typos, fmt.Sprintf("%s (%s)", row.ID, row.Title))
			}
		}

		out = append(out, d)
	}

	return out
}

// shippingIDs is the task's members and every subtask, closed ones kept,
// since a parent ships through its subtasks.
func shippingIDs(ctx context.Context, tr stack.Tracker, r ref.Ref) map[string]bool {
	ids := map[string]bool{}
	for _, id := range r.Members() {
		ids[strings.ToUpper(id)] = true
	}

	if tr.AllChildren == nil {
		return ids
	}

	if rows, err := tr.AllChildren(ctx, r); err == nil {
		for _, row := range rows {
			ids[strings.ToUpper(row.ID)] = true
		}
	}

	return ids
}

// openTasks is the tracker's open tasks; a failed read is none.
func openTasks(ctx context.Context, t *target.Target) []hostops.Row {
	rows, err := tasksource.TasksFor(t).List(ctx, openListed)
	if err != nil {
		return nil
	}

	out := rows[:0]

	for _, row := range rows {
		if !strings.EqualFold(row.Status, "closed") {
			out = append(out, row)
		}
	}

	return out
}

// shipped reads the default branch's commits that name one of ids, by a
// Task: trailer or a subject that opens with the id.
func shipped(ctx context.Context, dir, prefix string, ids map[string]bool) launch.Closed {
	log := gitx.Out(ctx, dir, "log", regression.DefaultRef(ctx, dir),
		"--format=%H%x1f%h%x1f%s%x1f%(trailers:key="+lore.TrailerTask+",valueonly,separator=%x20)%x1e", "--")

	d, newest := matchShipped(log, prefix, ids)
	if newest != "" {
		tags := gitx.Out(ctx, dir, "tag", "--contains", newest, "--sort=creatordate")
		d.Release, _, _ = strings.Cut(tags, "\n")
	}

	return d
}

// matchShipped picks the log's commits naming one of ids, newest first, and
// the full sha of the newest; log is shipped's format.
func matchShipped(log, prefix string, ids map[string]bool) (launch.Closed, string) {
	var (
		d      launch.Closed
		newest string
	)

	for rec := range strings.SplitSeq(log, "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(f) < 4 {
			continue
		}

		named := ids[strings.ToUpper(regression.SubjectTask(prefix, f[2]))]
		for id := range strings.FieldsSeq(f[3]) {
			named = named || ids[strings.ToUpper(id)]
		}

		if !named {
			continue
		}

		if newest == "" {
			newest = f[0]
		}

		if len(d.Shipped) < shippedShown {
			d.Shipped = append(d.Shipped, f[1]+" "+f[2])
		} else {
			d.More++
		}
	}

	return d, newest
}

// oneDigitOff says b is a's id with exactly one digit changed: the typo
// that starts FT-530 for FT-570.
func oneDigitOff(a, b string) bool {
	ap, an, ok1 := cutNumber(a)
	bp, bn, ok2 := cutNumber(b)

	if !ok1 || !ok2 || !strings.EqualFold(ap, bp) || len(an) != len(bn) {
		return false
	}

	diff := 0

	for i := range an {
		if an[i] != bn[i] {
			diff++
		}
	}

	return diff == 1
}

// cutNumber splits an id at its last dash into the prefix and the digits.
func cutNumber(id string) (string, string, bool) {
	i := strings.LastIndex(id, "-")
	if i < 0 || i == len(id)-1 {
		return "", "", false
	}

	for _, r := range id[i+1:] {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}

	return id[:i], id[i+1:], true
}
