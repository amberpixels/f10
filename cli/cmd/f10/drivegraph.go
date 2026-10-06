package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// The drive's dependency graph. Each task has at most one base: the
// f10-after its branch records once it started, the After: line in its
// body before. The graph is a map of task to base, rebuilt on every run
// from those two places, so nothing about it is stored: the resume is the
// same command, and it reads the same answer.

// graph reads every unfinished task's base onto the task and returns the
// order the drive runs them in. A base outside the list must already be
// finished, and a cycle cannot run at all; both are refused before anything
// is created. The notes name a branch and a body that disagree.
func (in *driveInput) graph(ctx context.Context, tasks []driveTask) ([]int, []string, error) {
	var notes []string

	for i := range tasks {
		t := &tasks[i]
		if t.finished {
			continue
		}

		t.base = t.after

		if t.branch == "" {
			continue
		}

		// the branch's copy wins: it is what start recorded, and what ship
		// and pr act on; finish clears it, and the body then names a base
		// that is finished, which is the same answer
		recorded := gitx.Out(ctx, in.main, "config", "--get", "branch."+t.branch+"."+cfgAfter)
		if recorded == "" {
			continue
		}

		if t.after != "" && !strings.EqualFold(recorded, t.after) {
			notes = append(
				notes,
				fmt.Sprintf("%s: its branch records after %s and its body says After: %s; the branch wins",
					t.ref.ID, recorded, t.after),
			)
		}

		t.base = recorded
	}

	index := listIndex(tasks)

	for _, t := range tasks {
		if t.finished || t.base == "" {
			continue
		}

		if _, listed := index[t.base]; listed {
			continue
		}

		done, err := in.baseFinished(ctx, t.base)
		if err != nil {
			return nil, nil, err
		}

		if !done {
			return nil, nil, fmt.Errorf("%s is After: %s, which is not in the list and not finished: "+
				"add %s to the list, or finish it first", t.ref.ID, t.base, t.base)
		}
	}

	order, err := topoOrder(tasks, index)
	if err != nil {
		return nil, nil, err
	}

	return order, notes, nil
}

// listIndex maps each listed id to its first position.
func listIndex(tasks []driveTask) map[string]int {
	index := make(map[string]int, len(tasks))

	for i, t := range tasks {
		if _, seen := index[t.ref.ID]; !seen {
			index[t.ref.ID] = i
		}
	}

	return index
}

// topoOrder puts every base before its dependents. Among the tasks ready at
// once, the one listed first goes first, so a list already in order runs in
// that order. Tasks left over when none is ready wait on each other.
func topoOrder(tasks []driveTask, index map[string]int) ([]int, error) {
	placed := make([]bool, len(tasks))
	order := make([]int, 0, len(tasks))

	ready := func(t driveTask) bool {
		b, listed := index[t.base]

		return t.finished || t.base == "" || !listed || placed[b]
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
					stuck = append(stuck, t.ref.ID+" after "+t.base)
				}
			}

			return nil, fmt.Errorf("the dependencies run in a circle: %s", strings.Join(stuck, ", "))
		}

		placed[next] = true
		order = append(order, next)
	}

	return order, nil
}
