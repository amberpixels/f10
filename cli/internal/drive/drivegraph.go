package drive

import (
	"context"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/textx"
)

// A task's base is the f10-after its branch records once started, else the
// command line's typed base, else its body's After: line. A join has several
// bases (f10-join, else the typed ones) and no single base. The graph is
// rebuilt every run from those, so a resume reads the same answer.

// graph refuses a base outside the list that is neither started nor
// finished, and a cycle, before anything is created.
func (in *DriveInput) graph(ctx context.Context, tasks []DriveTask) ([]int, []string, error) {
	var notes []string

	for i := range tasks {
		t := &tasks[i]
		if t.finished {
			continue
		}

		t.Base = t.after

		if t.Branch == "" {
			continue
		}

		join, err := branchmeta.Get(ctx, in.Main, t.Branch, branchmeta.Join)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: reading its recorded bases: %w", t.Ref.ID, err)
		}

		if join != "" {
			t.Base, t.Join = "", strings.Fields(join)

			continue
		}

		// the branch's copy wins: it is what start recorded, and what ship
		// and pr act on; finish clears it, and the body then names a base
		// that is finished, which is the same answer
		recorded, err := branchmeta.Get(ctx, in.Main, t.Branch, branchmeta.After)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: reading its recorded base: %w", t.Ref.ID, err)
		}

		if recorded == "" {
			continue
		}

		if t.after != "" && !strings.EqualFold(recorded, t.after) {
			notes = append(
				notes,
				fmt.Sprintf("%s: its branch records after %s and its body says After: %s; the branch wins",
					t.Ref.ID, recorded, t.after),
			)
		}

		t.Base = recorded
	}

	index := listIndex(tasks)

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
			return nil, nil, fmt.Errorf("%s is After: %s, which is not in the list, not started and not finished: "+
				"add %s to the list, or start it first", t.Ref.ID, t.Base, t.Base)
		}
	}

	order, err := topoOrder(tasks, index)
	if err != nil {
		return nil, nil, err
	}

	return order, notes, nil
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

	if in.Args.PlanOnly && in.Storage.PlanLive(base.ID) {
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

	return stack.FinishedTask(info, in.Tracker.Storage, base.ID, branched), nil
}
