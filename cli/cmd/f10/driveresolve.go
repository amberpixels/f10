package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// resolveTasks skips a range member the tracker does not know as a task,
// with a note; a listed one is an error.
func (in *driveInput) resolveTasks(ctx context.Context) ([]driveTask, []string, error) {
	var (
		tasks []driveTask
		notes []string
	)

	// listed is the task's place on the command line, -1 for one a range spans
	add := func(r ref.Ref, suffix string, listed int) error {
		ranged := listed < 0

		if i := slices.IndexFunc(tasks, func(t driveTask) bool {
			return t.suffix == suffix && slices.ContainsFunc(r.Members(), func(id string) bool {
				return slices.Contains(t.ref.Members(), id)
			})
		}); i >= 0 {
			return fmt.Errorf("%s is listed twice", sharedMember(tasks[i].ref, r))
		}

		info, err := in.tracker.Info(ctx, r)

		switch {
		case err != nil && ranged:
			notes = append(notes, fmt.Sprintf("%s skipped: %v", r.ID, err))

			return nil
		case err != nil:
			return fmt.Errorf("%s: %w", r.ID, err)
		case info.Pull && ranged:
			notes = append(notes, r.ID+" skipped: a pull request, not a task")

			return nil
		case info.Pull:
			return fmt.Errorf("%s is a pull request, not a task", r.ID)
		}

		task := driveTask{
			ref: r, suffix: suffix, info: info,
			profile: in.profileFor(listed), flagged: in.profileFlagged(listed),
		}

		line, err := stack.BundleAfter(ctx, in.tracker, r, info.Body)
		if err != nil {
			return err
		}

		if line != "" {
			base, err := in.tracker.Resolve(ctx, line)
			if err != nil {
				return fmt.Errorf("%s's body says After: %s: %w", r.ID, line, err)
			}

			task.after = base.Head().ID
		}

		x, err := stack.LoadBranchIndex(ctx, in.main)
		if err != nil {
			return err
		}

		first, err := x.Any(ctx, r.ID)
		if err != nil {
			return err
		}

		branched := first != ""
		if branched {
			var local []string
			if suffix == "" {
				local = x.Linked(r.ID)
			}

			if len(local) == 0 {
				if local, err = x.Under(ctx, "refs/heads/", r.ID, suffix); err != nil {
					return err
				}
			}

			if len(local) == 1 {
				task.branch = local[0]
			}
		}

		task.finished = stack.FinishedTask(info, in.tracker.Storage, r.ID, branched)

		if !ranged && !task.finished && !branched {
			for _, m := range r.MemberRefs() {
				if err := in.refuseParent(ctx, m); err != nil {
					return err
				}
			}
		}

		tasks = append(tasks, task)

		return nil
	}

	if in.args.Span {
		from, to, err := in.span(ctx)
		if err != nil {
			return nil, nil, err
		}

		for _, r := range spanRefs(from, to) {
			if err := add(r, "", -1); err != nil {
				return nil, nil, err
			}
		}

		return tasks, notes, nil
	}

	for i, tok := range in.args.Tasks {
		id, suffix := ref.SplitSuffix(tok)

		r, err := in.tracker.Resolve(ctx, id)
		if err != nil {
			return nil, nil, err
		}

		if suffix != "" && r.Bundle != nil {
			return nil, nil, fmt.Errorf("%s ships in bundle %s, which takes no suffix", r.ID, r.Label())
		}

		// any member drives the whole bundle, under its lead
		if err := add(r.Head(), suffix, i); err != nil {
			return nil, nil, err
		}
	}

	return tasks, notes, nil
}

// refuseParent stops a parent with unfinished subtasks: the subtasks are what
// runs, chosen in the drive skill. A started task is never a parent here, so
// a resumed drive asks the tracker nothing more.
func (in *driveInput) refuseParent(ctx context.Context, r ref.Ref) error {
	open, err := openSubtasks(ctx, in.tracker, r)
	if err != nil || len(open) == 0 {
		return err
	}

	return fmt.Errorf("%s is a parent of %s: list the subtasks to drive (f10 task children %s), "+
		"or run /f10:drive %s to choose", r.ID, strings.Join(open, ", "), r.ID, r.ID)
}

func (in *driveInput) span(ctx context.Context) (ref.Ref, ref.Ref, error) {
	if slices.ContainsFunc(in.args.Tasks, func(t string) bool { return strings.Contains(t, ref.Separator) }) {
		return ref.Ref{}, ref.Ref{}, errors.New("a range's ends are single ids: list a bundle on its own")
	}
	from, err := in.tracker.Resolve(ctx, in.args.Tasks[0])
	if err != nil {
		return ref.Ref{}, ref.Ref{}, err
	}

	to, err := in.tracker.Resolve(ctx, in.args.Tasks[1])
	if err != nil {
		return ref.Ref{}, ref.Ref{}, err
	}

	a, _ := strconv.Atoi(from.Number)
	b, _ := strconv.Atoi(to.Number)

	switch {
	case worktree.PrefixOf(from) != worktree.PrefixOf(to):
		return ref.Ref{}, ref.Ref{}, fmt.Errorf("a range stays in one id format: %s -- %s", from.ID, to.ID)
	case b < a:
		return ref.Ref{}, ref.Ref{}, fmt.Errorf("a range runs upwards: %s -- %s", from.ID, to.ID)
	case b-a >= maxSpan:
		return ref.Ref{}, ref.Ref{}, fmt.Errorf(
			"%s -- %s is %d ids; a range takes at most %d",
			from.ID,
			to.ID,
			b-a+1,
			maxSpan,
		)
	}

	return from, to, nil
}

func spanRefs(from, to ref.Ref) []ref.Ref {
	a, _ := strconv.Atoi(from.Number)
	b, _ := strconv.Atoi(to.Number)
	prefix := worktree.PrefixOf(from)

	refs := make([]ref.Ref, 0, b-a+1)

	for n := a; n <= b; n++ {
		num := strconv.Itoa(n)
		refs = append(refs, ref.Ref{ID: prefix + num, Number: num, Origin: ref.OriginExplicit})
	}

	return refs
}

func (in *driveInput) baseFinished(ctx context.Context, id string) (bool, error) {
	base, err := in.tracker.Resolve(ctx, id)
	if err != nil {
		return false, err
	}

	branched, err := stack.HasTaskBranch(ctx, in.main, base.ID)
	if err != nil || branched {
		return false, err
	}

	info, err := in.tracker.Info(ctx, base)
	if err != nil {
		return false, fmt.Errorf("%s: %w", base.ID, err)
	}

	return stack.FinishedTask(info, in.tracker.Storage, base.ID, false), nil
}

func sharedMember(a, b ref.Ref) string {
	for _, id := range b.Members() {
		if slices.Contains(a.Members(), id) {
			return id
		}
	}

	return b.ID
}
