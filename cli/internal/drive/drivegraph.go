package drive

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/drivectl"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/textx"
)

// A task's bases are settled once, in settleBases. A started task takes its
// branch's record, and any other what the command line or the body asks.
// One base is a stack (Base) and several a join (Join), never both.
// The graph is rebuilt every run from those, so a resume reads the same answer.

// A baseSource is who named a task's bases.
type baseSource int

const (
	baseNone   baseSource = iota
	baseBody              // the body's After: line
	baseLine              // a typed order: A,B or C@A
	baseFlag              // --after or --base, which restacks the one task
	baseBranch            // the branch's own record
)

// names is how a message says the source asked for bases.
func (s baseSource) names(bases []string) string {
	list := textx.JoinAnd(bases)

	switch s {
	case baseBody:
		return "its body says After: " + list
	case baseLine:
		return "the command line names " + list
	case baseFlag:
		return "--after names " + list
	case baseBranch:
		return "its branch records after " + list
	case baseNone:
	}

	return list
}

// graph refuses a base outside the list that is neither started nor
// finished, and a cycle, before anything is created.
func (in *DriveInput) graph(ctx context.Context, tasks []DriveTask) ([]int, []string, error) {
	var notes []string

	index := listIndex(tasks)

	for i := range tasks {
		note, err := in.settleBases(ctx, &tasks[i], tasks, index)
		if err != nil {
			return nil, nil, err
		}

		if note != "" {
			notes = append(notes, note)
		}
	}

	if err := in.refuseJoins(ctx, tasks, index); err != nil {
		return nil, nil, err
	}

	for _, t := range tasks {
		if t.finished || t.Base == "" {
			continue
		}

		if _, listed := index[t.Base]; listed {
			continue
		}

		// a started base outside the list is stacked on as it stands, the way
		// --after stacks one task on a task in progress
		ok, err := in.baseOutside(ctx, t.Base)
		if err != nil {
			return nil, nil, err
		}

		if !ok {
			return nil, nil, fmt.Errorf("%s: %s, which is not in the list, not started and not finished: "+
				"add %s to the list, or start it first", t.Ref.ID, t.source.names([]string{t.Base}), t.Base)
		}
	}

	order, err := topoOrder(tasks, index)
	if err != nil {
		return nil, nil, err
	}

	return order, notes, nil
}

// settleBases sets a task's Base or Join, the one place either is set. The
// branch's record wins over a typed order and the body, because it is what
// ship, pr and finish act on. Only --after and --base win over it, because
// they restack the one task they name. The note says what was overruled.
func (in *DriveInput) settleBases(
	ctx context.Context,
	t *DriveTask,
	tasks []DriveTask,
	index map[string]int,
) (string, error) {
	t.Base, t.Join = "", nil

	if t.finished {
		return "", nil
	}

	bases, source, note := leads(t.want, tasks, index, t.Ref), t.source, ""

	if t.Branch != "" && source != baseFlag {
		rec, err := branchmeta.Load(ctx, in.Main, t.Branch)
		if err != nil {
			return "", fmt.Errorf("%s: reading its recorded bases: %w", t.Ref.ID, err)
		}

		// finish clears the record, and the body then names a base that is
		// finished, which is the same answer
		recorded := rec.Join
		if rec.After != "" {
			recorded = []string{rec.After}
		}

		if recorded = leads(recorded, tasks, index, t.Ref); len(recorded) > 0 {
			if len(bases) > 0 && !sameIDs(recorded, bases) {
				note = fmt.Sprintf("%s: %s and %s; the branch wins",
					t.Ref.ID, baseBranch.names(recorded), source.names(bases))
			}

			bases, source = recorded, baseBranch
		}
	}

	t.source = source

	if len(bases) == 1 {
		t.Base = bases[0]
	} else {
		t.Join = bases
	}

	return note, nil
}

// leads names each base by the lead of the listed entry that holds it, once,
// so a bundle's rider named as a base is waited for as the bundle. A base in
// the task's own entry orders work inside its branch and is no base.
func leads(bases []string, tasks []DriveTask, index map[string]int, own ref.Ref) []string {
	var out []string

	for _, id := range bases {
		if i, listed := index[id]; listed {
			id = tasks[i].Ref.ID
		}

		if id == "" || slices.Contains(own.Members(), id) ||
			slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, id) }) {
			continue
		}

		out = append(out, id)
	}

	return out
}

// sameIDs compares two id lists, ignoring order and case.
func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for _, x := range a {
		if !slices.ContainsFunc(b, func(y string) bool { return strings.EqualFold(x, y) }) {
			return false
		}
	}

	return true
}

// listIndex maps each listed id to its first position; every member of a
// bundle maps to the bundle's, so a base that rides in it waits for it.
func listIndex(tasks []DriveTask) map[string]int {
	index := make(map[string]int, len(tasks))

	for i, t := range tasks {
		for _, id := range t.Ref.Members() {
			if _, seen := index[id]; !seen {
				index[id] = i
			}
		}
	}

	return index
}

// topoOrder puts bases before dependents; among the ready, the one listed
// first goes first, so a list already in order runs in that order.
func topoOrder(tasks []DriveTask, index map[string]int) ([]int, error) {
	placed := make([]bool, len(tasks))
	order := make([]int, 0, len(tasks))

	ready := func(t DriveTask) bool {
		if t.finished {
			return true
		}

		for _, id := range append([]string{t.Base}, t.Join...) {
			if b, listed := index[id]; id != "" && listed && !placed[b] {
				return false
			}
		}

		return true
	}

	for len(order) < len(tasks) {
		next := -1

		for i, t := range tasks {
			if !placed[i] && ready(t) {
				next = i

				break
			}
		}

		if next < 0 {
			var stuck []string

			for i, t := range tasks {
				if !placed[i] {
					stuck = append(stuck, t.Ref.ID+" after "+strings.Join(t.bases(), ", "))
				}
			}

			return nil, fmt.Errorf("the dependencies run in a circle: %s", strings.Join(stuck, ", "))
		}

		placed[next] = true
		order = append(order, next)
	}

	return order, nil
}

// bases is every base the task waits for: its one base, or a join's.
func (t DriveTask) bases() []string {
	if t.Base != "" {
		return []string{t.Base}
	}

	return t.Join
}

// refuseJoins refuses a join nothing in this batch would let start, before
// anything is created. Only finish merges a join's bases, so a flow without
// it is refused, and so is an unmerged base outside the list. Under --plan a
// join opens on its bases' plans, so an outside base needs a saved plan.
func (in *DriveInput) refuseJoins(ctx context.Context, tasks []DriveTask, index map[string]int) error {
	for _, t := range tasks {
		// a started join is checked too, because under --plan it branched
		// before its bases merged, and a later run must still find them
		if t.finished || len(t.Join) == 0 {
			continue
		}

		if !in.Args.PlanOnly && (!in.Flow.Finishes || in.Flow.Mixed) {
			return fmt.Errorf("%s waits for %s to merge, and the %s flow does not end in finish, "+
				"so nothing would merge them: run a flow that finishes, or stack it on one base",
				t.Ref.ID, textx.JoinAnd(t.Join), in.Flow.Label())
		}

		for _, id := range t.Join {
			if _, listed := index[id]; listed {
				continue
			}

			ok, err := in.joinOutside(ctx, id)
			if err != nil {
				return err
			}

			if !ok {
				return fmt.Errorf("%s waits for %s to merge, which is not in the list and not finished, "+
					"so nothing in this batch would merge it: add %s to the list, or finish it first",
					t.Ref.ID, id, id)
			}
		}
	}

	return nil
}

// joinOutside says a join's base the list does not hold is no wait: it is
// finished, or under --plan its plan is saved.
func (in *DriveInput) joinOutside(ctx context.Context, id string) (bool, error) {
	base, err := in.Tracker.Resolve(ctx, id)
	if err != nil {
		return false, err
	}

	b := drivectl.BaseStanding{Planned: in.Storage.PlanLive(base.ID)}
	if b.Through(in.Args.PlanOnly) {
		return true, nil
	}

	branched, err := stack.HasTaskBranch(ctx, in.Main, base.ID)
	if err != nil {
		return false, err
	}

	info, err := in.Tracker.Info(ctx, base)
	if err != nil {
		return false, fmt.Errorf("%s: %w", base.ID, err)
	}

	b.Finished = stack.FinishedTask(info, in.Tracker.Storage, base.ID, branched)

	return b.Through(in.Args.PlanOnly), nil
}
