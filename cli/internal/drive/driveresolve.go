package drive

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// resolveTasks skips a range member the tracker does not know as a task,
// with a note; a listed one is an error.
func (in *DriveInput) resolveTasks(ctx context.Context) ([]DriveTask, []string, error) {
	var (
		tasks []DriveTask
		notes []string
	)

	// listed is the task's place on the command line, -1 for one a range spans
	add := func(r ref.Ref, suffix string, listed int) error {
		ranged := listed < 0

		if i := slices.IndexFunc(tasks, func(t DriveTask) bool {
			return t.Suffix == suffix && slices.ContainsFunc(r.Members(), func(id string) bool {
				return slices.Contains(t.Ref.Members(), id)
			})
		}); i >= 0 {
			return fmt.Errorf("%s is listed twice", sharedMember(tasks[i].Ref, r))
		}

		info, err := in.Tracker.Info(ctx, r)

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

		task := DriveTask{
			Ref: r, Suffix: suffix, info: info,
			profile: in.profileFor(listed), flagged: in.profileFlagged(listed),
		}

		line, err := stack.BundleAfter(ctx, in.Tracker, r, info.Body)
		if err != nil {
			return err
		}

		if line != "" {
			base, err := in.Tracker.Resolve(ctx, line)
			if err != nil {
				return fmt.Errorf("%s's body says After: %s: %w", r.ID, line, err)
			}

			task.after = base.Head().ID
		}

		x, err := stack.LoadBranchIndex(ctx, in.Main)
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
				task.Branch = local[0]
			}
		}

		task.finished = stack.FinishedTask(info, in.Tracker.Storage, r.ID, branched)

		if !ranged && !task.finished && !branched && len(r.Riders()) > 0 {
			for _, m := range r.MemberRefs() {
				if err := in.refuseParent(ctx, m); err != nil {
					return err
				}
			}
		}

		tasks = append(tasks, task)

		return nil
	}

	if in.Args.Span {
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

	for i, tok := range in.Args.Tasks {
		id, suffix := ref.SplitSuffix(tok)

		r, err := in.Tracker.Resolve(ctx, id)
		if err != nil {
			return nil, nil, err
		}

		if suffix != "" && r.Bundle != nil {
			return nil, nil, fmt.Errorf("%s ships in bundle %s, which takes no suffix", r.ID, r.Label())
		}

		subs, left, err := in.subtasks(ctx, r, suffix)
		if err != nil {
			return nil, nil, err
		}

		notes = append(notes, left...)

		for _, sub := range subs {
			if err := add(sub, "", i); err != nil {
				return nil, nil, err
			}
		}

		if subs != nil {
			continue
		}

		// any member drives the whole bundle, under its lead
		if err := add(r.Head(), suffix, i); err != nil {
			return nil, nil, err
		}
	}

	return tasks, notes, in.typedBase(ctx, tasks)
}

// typedBase lays --after or --base over the one task's body: --after names
// its base task, --base a branch, which leaves it no base task.
func (in *DriveInput) typedBase(ctx context.Context, tasks []DriveTask) error {
	switch {
	case in.Args.After == "" && in.Args.Base == "":
		return nil
	case len(tasks) != 1:
		return fmt.Errorf("--after and --base set one task's base, and %s runs %d tasks", in.Args.Tasks[0], len(tasks))
	case in.Args.Base != "":
		tasks[0].after = ""
	case in.Args.After != "":
		base, err := in.Tracker.Resolve(ctx, in.Args.After)
		if err != nil {
			return fmt.Errorf("--after: %w", err)
		}

		tasks[0].after = base.Head().ID
	}

	return nil
}

// subtasks is what a listed parent runs instead of itself: its subtasks in
// After: order, a finished one reported as such and an open draft left out
// with a note, since a draft is an idea not ready to plan. nil means r runs
// itself: it has no subtask, or a branch, so it was started on its own.
func (in *DriveInput) subtasks(ctx context.Context, r ref.Ref, suffix string) ([]ref.Ref, []string, error) {
	read := in.Tracker.AllChildren
	if read == nil {
		read = in.Tracker.Children
	}

	if suffix != "" || len(r.Riders()) > 0 || read == nil {
		return nil, nil, nil
	}

	x, err := stack.LoadBranchIndex(ctx, in.Main)
	if err != nil {
		return nil, nil, err
	}

	if first, err := x.Any(ctx, r.ID); err != nil || first != "" {
		return nil, nil, err
	}

	rows, err := read(ctx, r)
	if errors.Is(err, tasksource.ErrNoParentLink) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("%s: reading its subtasks: %w", r.ID, err)
	}

	if len(rows) == 0 {
		return nil, nil, nil
	}

	var (
		subs  []ref.Ref
		notes []string
	)

	for _, row := range rows {
		draft := slices.ContainsFunc(row.Labels, func(l string) bool { return strings.EqualFold(l, "draft") })
		if draft && !row.Finished {
			notes = append(notes, fmt.Sprintf("%s left out: a draft subtask of %s; list it to run it", row.ID, r.ID))

			continue
		}

		sub, err := in.Tracker.Resolve(ctx, row.ID)
		if err != nil {
			return nil, nil, err
		}

		subs = append(subs, sub.Head())
	}

	if len(subs) == 0 {
		return nil, nil, fmt.Errorf("%s's subtasks are all drafts: list the ones to run", r.ID)
	}

	return subs, notes, nil
}

// refuseParent stops a bundle member with unfinished subtasks, since one
// branch would ship the whole umbrella. A started task is never a parent
// here, so a resumed drive asks the tracker nothing more.
func (in *DriveInput) refuseParent(ctx context.Context, r ref.Ref) error {
	open, err := stack.OpenSubtasks(ctx, in.Tracker, r)
	if err != nil || len(open) == 0 {
		return err
	}

	return fmt.Errorf("%s is a parent of %s: start the parent alone, or list the subtasks to bundle",
		r.ID, strings.Join(open, ", "))
}

func (in *DriveInput) span(ctx context.Context) (ref.Ref, ref.Ref, error) {
	if slices.ContainsFunc(in.Args.Tasks, func(t string) bool { return strings.Contains(t, ref.Separator) }) {
		return ref.Ref{}, ref.Ref{}, errors.New("a range's ends are single ids: list a bundle on its own")
	}
	from, err := in.Tracker.Resolve(ctx, in.Args.Tasks[0])
	if err != nil {
		return ref.Ref{}, ref.Ref{}, err
	}

	to, err := in.Tracker.Resolve(ctx, in.Args.Tasks[1])
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

func (in *DriveInput) baseFinished(ctx context.Context, id string) (bool, error) {
	base, err := in.Tracker.Resolve(ctx, id)
	if err != nil {
		return false, err
	}

	branched, err := stack.HasTaskBranch(ctx, in.Main, base.ID)
	if err != nil || branched {
		return false, err
	}

	info, err := in.Tracker.Info(ctx, base)
	if err != nil {
		return false, fmt.Errorf("%s: %w", base.ID, err)
	}

	return stack.FinishedTask(info, in.Tracker.Storage, base.ID, false), nil
}

func sharedMember(a, b ref.Ref) string {
	for _, id := range b.Members() {
		if slices.Contains(a.Members(), id) {
			return id
		}
	}

	return b.ID
}
