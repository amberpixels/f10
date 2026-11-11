package start

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/fsx"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/launch"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/ui"
	"github.com/amberpixels/f10/cli/internal/worktree"
)

// AfterFromBody reads the task's body for an After: line. A body that
// cannot be read is a note, never a refusal: --after stays the explicit way.
func AfterFromBody(ctx context.Context, tr stack.Tracker, main string, task ref.Ref) (string, []string, error) {
	info, err := tr.Info(ctx, task)
	if err != nil {
		return "", []string{fmt.Sprintf("no body read for %s (%v), so no After: line applies", task.ID, err)}, nil
	}

	after, note, err := stack.BodyAfter(ctx, tr, main, task, info.Body)
	if err != nil || note == "" {
		return after, nil, err
	}

	return after, []string{note}, nil
}

// A baseRef is what the worktree is created from: the ref as git takes it,
// the flag that named it, and the commit it points at, which the note about
// a base that changes nothing compares to the default branch.
type baseRef struct {
	ref  string
	flag string
	sha  string
}

// ChecksParent reports whether start refuses a parent: a PR pickup is
// someone else's branch, and talking an umbrella task through before
// splitting it is what --discuss is for.
func ChecksParent(mode string, pick *hostops.Pickup) bool {
	return pick == nil && mode != launch.ModeDiscuss
}

// RefuseStartParent stops a parent with unfinished subtasks before anything
// is created, since its agent would ship the whole umbrella on one branch.
// A task with a branch was started already and reopens.
func RefuseStartParent(ctx context.Context, in StartInput) error {
	if in.Children == nil {
		return nil
	}

	branched, err := stack.HasTaskBranch(ctx, in.Main, in.Task.ID)
	if err != nil || branched {
		return err
	}

	for _, m := range in.Task.MemberRefs() {
		open, err := stack.OpenSubtasks(ctx, stack.Tracker{Children: in.Children}, m)
		if err != nil {
			return err
		}

		if len(open) > 0 {
			return ui.Exit(ui.ExitParent, "%s is a parent of %s: run /f10:drive %s to drive them, "+
				"or start one subtask (f10 start %s)", m.ID, strings.Join(open, ", "), m.ID, open[0])
		}
	}

	return nil
}

// SettleBundle refuses what would split a task across two branches, a rider
// another branch ships or a branch reopened with other riders than typed,
// since riders are fixed at start; a task alone takes its branch's riders.
func SettleBundle(ctx context.Context, main string, task ref.Ref, existing string) (ref.Ref, error) {
	x, err := stack.LoadBranchIndex(ctx, main)
	if err != nil {
		return ref.Ref{}, err
	}

	if existing != "" {
		lead, riders := x.Bundle(existing, strings.TrimSuffix(worktree.PrefixOf(task), "-"))

		switch {
		case len(task.Riders()) == 0 && len(riders) > 0 && strings.EqualFold(lead, task.ID):
			task.Bundle = append([]string{task.ID}, riders...)
		case !strings.EqualFold(lead, task.ID) && lead != "":
			return ref.Ref{}, fmt.Errorf("%s is already on branch %s, led by %s: finish it first, or start %s",
				task.ID, existing, lead, lead)
		case !slices.Equal(riders, task.Riders()):
			return ref.Ref{}, fmt.Errorf("branch %s ships %s; riders are fixed at start, so %s cannot reopen it: "+
				"finish it first, or start %s", existing,
				ref.Ref{ID: task.ID, Bundle: append([]string{task.ID}, riders...)}.Label(),
				task.Label(), task.ID)
		}
	}

	for _, rider := range task.Riders() {
		branches := x.Linked(rider)

		for _, root := range []string{"refs/heads/", "refs/remotes/origin/"} {
			refs, err := gitx.Refs(ctx, main, append(stack.TaskRefs(root, rider), stack.RiderRefs(root, rider)...)...)
			if err != nil {
				return ref.Ref{}, err
			}

			for _, r := range refs {
				branches = append(branches, strings.TrimPrefix(r, "origin/"))
			}
		}

		for _, b := range branches {
			if b != existing {
				return ref.Ref{}, fmt.Errorf("%s is already on branch %s: finish it first, or leave it out of %s",
					rider, b, task.Label())
			}
		}
	}

	return task, nil
}

// recordBundle links the branch to its lead and lists its riders, under
// the branch's section of the local git config, so any member finds it.
func recordBundle(ctx context.Context, dir, branch string, task ref.Ref) error {
	if err := branchmeta.Set(ctx, dir, branch, branchmeta.Task, task.ID); err != nil {
		return fmt.Errorf("recording bundle %s: %w", task.Label(), err)
	}

	if err := branchmeta.Set(ctx, dir, branch, branchmeta.Riders, strings.Join(task.Riders(), " ")); err != nil {
		return fmt.Errorf("recording bundle %s: %w", task.Label(), err)
	}

	return nil
}

// baseFor turns --after or --base into the ref the worktree is created
// from, and for --after the dependency to record. Neither given is an empty
// base, which lets git choose.
func baseFor(ctx context.Context, in StartInput) (baseRef, *launch.Dependency, error) {
	switch {
	case in.After != "":
		x, err := stack.LoadBranchIndex(ctx, in.Main)
		if err != nil {
			return baseRef{}, nil, err
		}

		branch, err := x.After(ctx, in.After)
		if err != nil {
			return baseRef{}, nil, err
		}

		base := baseRef{ref: branch, flag: "--after", sha: stack.RefSHA(ctx, in.Main, branch)}
		dep := &launch.Dependency{Task: in.After, Branch: strings.TrimPrefix(branch, "origin/")}

		return base, dep, nil
	case in.Base != "":
		branch, sha, err := stack.NamedRef(ctx, in.Main, in.Base)
		if err != nil {
			return baseRef{}, nil, err
		}

		return baseRef{ref: branch, flag: "--base", sha: sha}, nil, nil
	default:
		return baseRef{}, nil, nil
	}
}

// ignoredBaseNote says the base flag did not apply because the branch
// exists and keeps the base it was created from. A dependency is still
// recorded, and the note says so, since it is what ship and pr act on.
func ignoredBaseNote(base baseRef, dep *launch.Dependency, name string) string {
	if dep == nil {
		return fmt.Sprintf("%s ignored: branch %s already exists and keeps its base", base.flag, name)
	}

	return fmt.Sprintf("%s ignored for the base: branch %s already exists and keeps it; dependency on %s recorded",
		base.flag, name, dep.Task)
}

// sameAsDefaultNote names a base that sits at the default branch's commit:
// for --after, the base task has no code yet, which the dependent agent is
// told to design against rather than discover.
func sameAsDefaultNote(ctx context.Context, dir string, base baseRef, dep *launch.Dependency) string {
	if base.sha == "" {
		return ""
	}

	def := stack.DefaultBranch(ctx, dir)
	if def == "" || stack.RefSHA(ctx, dir, "refs/remotes/"+def) != base.sha {
		return ""
	}

	if dep != nil {
		return fmt.Sprintf("%s %s: branch %s is at the same commit as the default branch %s, so %s has no code yet",
			base.flag, dep.Task, base.ref, def, dep.Task)
	}

	return fmt.Sprintf("%s %s is at the same commit as the default branch %s", base.flag, base.ref, def)
}

// locatePlan finds the base task's plan in the shared plans dir. The notes
// name what was not found, so a prompt that cannot cite the plan is explained.
func locatePlan(dep *launch.Dependency, root storage.Root) []string {
	dep.Plan = root.Plan(dep.Task)

	if _, err := os.Stat(dep.Plan); err != nil {
		return []string{fmt.Sprintf("%s has no plan yet at %s", dep.Task, dep.Plan)}
	}

	dep.Planned = true

	return nil
}

// planIf is path when a plan is there, else "".
func planIf(path string) string {
	if !fsx.Exists(path) {
		return ""
	}

	return path
}

// recordDependency writes the dependency under the branch's section of the
// local git config: per-branch, shared by every worktree, never pushed.
func recordDependency(ctx context.Context, dir, branch string, dep *launch.Dependency) error {
	if err := branchmeta.Set(ctx, dir, branch, branchmeta.After, dep.Task); err != nil {
		return fmt.Errorf("recording the dependency on %s: %w", dep.Task, err)
	}

	if err := branchmeta.Set(ctx, dir, branch, branchmeta.AfterBranch, dep.Branch); err != nil {
		return fmt.Errorf("recording the dependency on %s: %w", dep.Task, err)
	}

	return worktree.RecordTip(ctx, dir, branch, dep.Branch)
}
