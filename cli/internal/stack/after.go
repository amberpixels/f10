package stack

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// LinkAfter puts link in place of the value on body's first After: line.
func LinkAfter(body, link string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if hostops.AfterID(line) != "" {
			lines[i] = "After: " + link

			break
		}
	}

	return strings.Join(lines, "\n")
}

// A Tracker answers the questions start and drive ask of the task source.
// Funcs rather than a target, so the flows run under test without one.
type Tracker struct {
	Resolve func(ctx context.Context, token string) (ref.Ref, error)
	Info    func(ctx context.Context, task ref.Ref) (hostops.TaskInfo, error)
	// Children is the task's open subtasks; nil, like errNoParentLink, is a
	// tracker with no parent link
	Children func(ctx context.Context, task ref.Ref) ([]ChildRow, error)
	// AllChildren is children with the closed ones kept, for status
	AllChildren func(ctx context.Context, task ref.Ref) ([]ChildRow, error)
	Storage     storage.Root       // where a finished task's plan is archived
	titles      tasktitle.Template // tracker.title, whose prefix comes off each child's title
}

// TrackerFor binds the lookups to this target: the driver's `read` for the
// body where the project has one, the host's issue otherwise.
func TrackerFor(t *target.Target) Tracker {
	main := t.Layout.MainRoot
	if main == "" {
		main = t.Layout.CheckoutRoot
	}

	tr := Tracker{
		Resolve: t.Reference,
		Info: func(ctx context.Context, task ref.Ref) (hostops.TaskInfo, error) {
			return hostops.TasksFor(t).Info(ctx, task)
		},
		Storage: t.Layout.StorageRoot,
		titles:  t.Effective.TitleTemplate,
	}

	children := func(keepClosed bool) func(context.Context, ref.Ref) ([]ChildRow, error) {
		return func(ctx context.Context, task ref.Ref) ([]ChildRow, error) {
			raw, err := RawChildrenFor(ctx, t, task)
			if err != nil {
				return nil, err
			}

			rows, err := ShapeChildren(ctx, tr, main, raw, keepClosed)
			RememberRefsFor(ctx, t, ChildRefs(rows)...)

			return rows, err
		}
	}

	tr.Children, tr.AllChildren = children(false), children(true)

	return tr
}

// FinishedTask is the one "is this task done with": no branch, no live plan
// (a re-plan is pending work whatever the tracker says), and closed or its
// plan archived, which catches a merge whose PR body closed no issue.
func FinishedTask(info hostops.TaskInfo, root storage.Root, id string, hasBranch bool) bool {
	if hasBranch || root.PlanLive(id) {
		return false
	}

	return info.Closed || root.PlanArchived(id)
}

// HasTaskBranch reports a branch of the task, linked, local or on origin.
func HasTaskBranch(ctx context.Context, dir, id string) (bool, error) {
	x, err := LoadBranchIndex(ctx, dir)
	if err != nil {
		return false, err
	}

	branch, err := x.Any(ctx, id)

	return branch != "", err
}

// BodyAfter is the base the body's After: line gives start when no flag
// names one. A base with a branch is the dependency, a finished one is in
// the default branch already; anything else is refused before anything is made.
func BodyAfter(ctx context.Context, tr Tracker, main string, task ref.Ref, body string) (string, string, error) {
	line, err := BundleAfter(ctx, tr, task, body)
	if err != nil || line == "" {
		return "", "", err
	}

	base, err := tr.Resolve(ctx, line)
	if err != nil {
		return "", "", fmt.Errorf("%s's body says After: %s: %w", task.ID, line, err)
	}

	// a base that rides in a bundle is waited for as the bundle, by its lead
	base = base.Head()

	branched, err := HasTaskBranch(ctx, main, base.ID)
	if err != nil {
		return "", "", err
	}

	if branched {
		return base.ID, fmt.Sprintf("after %s, from the task body's After: line", base.ID), nil
	}

	info, err := tr.Info(ctx, base)
	if err != nil {
		return "", "", fmt.Errorf("%s's body says After: %s: %w", task.ID, base.ID, err)
	}

	if FinishedTask(info, tr.Storage, base.ID, false) {
		return "", fmt.Sprintf("After: %s is finished, so the branch is based on the default branch", base.ID), nil
	}

	return "", "", fmt.Errorf("%s's body says After: %s, which has no branch and is not finished: "+
		"start %s first, or pass --base", task.ID, base.ID, base.ID)
}

// BundleAfter is the After: line a task answers to: its body's, or for a
// bundle the one base its members name outside the bundle. A line naming
// another member orders work inside the branch; two bases cannot both be it.
func BundleAfter(ctx context.Context, tr Tracker, task ref.Ref, body string) (string, error) {
	if len(task.Riders()) == 0 {
		return hostops.AfterIn(body), nil
	}

	var line, base, from string

	for i, m := range task.MemberRefs() {
		text := body
		if i > 0 {
			info, err := tr.Info(ctx, m)
			if err != nil {
				return "", fmt.Errorf("reading %s for its After: line: %w", m.ID, err)
			}

			text = info.Body
		}

		l := hostops.AfterIn(text)
		if l == "" {
			continue
		}

		r, err := tr.Resolve(ctx, l)
		if err != nil {
			return "", fmt.Errorf("%s's body says After: %s: %w", m.ID, l, err)
		}

		id := r.Head().ID
		if slices.Contains(task.Members(), r.ID) || slices.Contains(task.Members(), id) {
			continue
		}

		switch {
		case base == "":
			line, base, from = l, id, m.ID
		case base != id:
			return "", fmt.Errorf("bundle %s has two bases: %s is After: %s and %s is After: %s; "+
				"a branch has one", task.Label(), from, base, m.ID, id)
		}
	}

	return line, nil
}
