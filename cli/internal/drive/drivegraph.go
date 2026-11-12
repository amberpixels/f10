package drive

import (
	"context"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
)

// Each task has at most one base: the f10-after its branch records once
// started, else its body's After: line. The graph is rebuilt every run from
// those, so nothing is stored and a resume reads the same answer.

// Graph refuses a base outside the list that is not finished, and a cycle,
// before anything is created.
func (in *DriveInput) Graph(ctx context.Context, tasks []DriveTask) ([]int, []string, error) {
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

	for _, t := range tasks {
		if t.finished || t.Base == "" {
			continue
		}

		if _, listed := index[t.Base]; listed {
			continue
		}

		done, err := in.baseFinished(ctx, t.Base)
		if err != nil {
			return nil, nil, err
		}

		if !done {
			return nil, nil, fmt.Errorf("%s is After: %s, which is not in the list and not finished: "+
				"add %s to the list, or finish it first", t.Ref.ID, t.Base, t.Base)
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
		b, listed := index[t.Base]

		return t.finished || t.Base == "" || !listed || placed[b]
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
					stuck = append(stuck, t.Ref.ID+" after "+t.Base)
				}
			}

			return nil, fmt.Errorf("the dependencies run in a circle: %s", strings.Join(stuck, ", "))
		}

		placed[next] = true
		order = append(order, next)
	}

	return order, nil
}
