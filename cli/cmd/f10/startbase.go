package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/storage"
)

// afterFromBody reads the task's body for an After: line. A body that
// cannot be read is a note, never a refusal: --after stays the explicit way.
func afterFromBody(ctx context.Context, tr tracker, main string, task ref.Ref) (string, []string, error) {
	info, err := tr.info(ctx, task)
	if err != nil {
		return "", []string{fmt.Sprintf("no body read for %s (%v), so no After: line applies", task.ID, err)}, nil
	}

	after, note, err := bodyAfter(ctx, tr, main, task, info.body)
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

// A dependency is what --after records on the new branch: the base task,
// its branch as a bare name (`origin/` stripped, since the pr step targets
// a branch and ship looks it up local-first), and where its plan is.
type dependency struct {
	task    string
	branch  string
	plan    string // the plan's path, or "" when no checkout can reach it
	planned bool   // the plan exists at that path
}

// checksParent reports whether start refuses a parent: a PR pickup is
// someone else's branch, and talking an umbrella task through before
// splitting it is what --discuss is for.
func checksParent(mode string, pick *pickup) bool {
	return pick == nil && mode != modeDiscuss
}

// refuseStartParent stops a parent with unfinished subtasks before anything
// is created, since its agent would ship the whole umbrella on one branch.
// A task with a branch was started already and reopens.
func refuseStartParent(ctx context.Context, in startInput) error {
	if in.children == nil {
		return nil
	}

	branched, err := hasTaskBranch(ctx, in.main, in.task.ID)
	if err != nil || branched {
		return err
	}

	for _, m := range in.task.MemberRefs() {
		open, err := openSubtasks(ctx, tracker{children: in.children}, m)
		if err != nil {
			return err
		}

		if len(open) > 0 {
			return cli.Exit(fmt.Sprintf("f10: %s is a parent of %s: run /f10:drive %s to drive them, "+
				"or start one subtask (f10 start %s)", m.ID, strings.Join(open, ", "), m.ID, open[0]), exitParent)
		}
	}

	return nil
}

// settleBundle refuses what would split a task across two branches, a rider
// another branch ships or a branch reopened with other riders than typed,
// since riders are fixed at start; a task alone takes its branch's riders.
func settleBundle(ctx context.Context, main string, task ref.Ref, existing string) (ref.Ref, error) {
	if existing != "" {
		lead, riders, err := branchBundle(ctx, main, existing, strings.TrimSuffix(prefixOf(task), "-"))
		if err != nil {
			return ref.Ref{}, err
		}

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
		branches, err := linkedBranches(ctx, main, rider)
		if err != nil {
			return ref.Ref{}, err
		}

		for _, root := range []string{"refs/heads/", "refs/remotes/origin/"} {
			refs, err := gitx.Refs(ctx, main, append(taskRefs(root, rider), riderRefs(root, rider)...)...)
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
	section := "branch." + branch + "."

	if err := gitx.SetConfig(ctx, dir, section+cfgTaskID, task.ID); err != nil {
		return fmt.Errorf("recording bundle %s: %w", task.Label(), err)
	}

	if err := gitx.SetConfig(ctx, dir, section+cfgRiders, strings.Join(task.Riders(), " ")); err != nil {
		return fmt.Errorf("recording bundle %s: %w", task.Label(), err)
	}

	return nil
}

// baseFor turns --after or --base into the ref the worktree is created
// from, and for --after the dependency to record. Neither given is an empty
// base, which lets git choose.
func baseFor(ctx context.Context, in startInput) (baseRef, *dependency, error) {
	switch {
	case in.after != "":
		branch, err := taskBranch(ctx, in.main, in.after)
		if err != nil {
			return baseRef{}, nil, err
		}

		base := baseRef{ref: branch, flag: "--after", sha: refSHA(ctx, in.main, branch)}
		dep := &dependency{task: in.after, branch: strings.TrimPrefix(branch, "origin/")}

		return base, dep, nil
	case in.base != "":
		branch, sha, err := namedRef(ctx, in.main, in.base)
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
func ignoredBaseNote(base baseRef, dep *dependency, name string) string {
	if dep == nil {
		return fmt.Sprintf("%s ignored: branch %s already exists and keeps its base", base.flag, name)
	}

	return fmt.Sprintf("%s ignored for the base: branch %s already exists and keeps it; dependency on %s recorded",
		base.flag, name, dep.task)
}

// sameAsDefaultNote names a base that sits at the default branch's commit:
// for --after, the base task has no code yet, which the dependent agent is
// told to design against rather than discover.
func sameAsDefaultNote(ctx context.Context, dir string, base baseRef, dep *dependency) string {
	if base.sha == "" {
		return ""
	}

	def := defaultBranch(ctx, dir)
	if def == "" || refSHA(ctx, dir, "refs/remotes/"+def) != base.sha {
		return ""
	}

	if dep != nil {
		return fmt.Sprintf("%s %s: branch %s is at the same commit as the default branch %s, so %s has no code yet",
			base.flag, dep.task, base.ref, def, dep.task)
	}

	return fmt.Sprintf("%s %s is at the same commit as the default branch %s", base.flag, base.ref, def)
}

// locatePlan finds the base task's plan in the shared plans dir. The notes
// name what was not found, so a prompt that cannot cite the plan is explained.
func locatePlan(dep *dependency, root storage.Root) []string {
	dep.plan = root.Plan(dep.task)

	if _, err := os.Stat(dep.plan); err != nil {
		return []string{fmt.Sprintf("%s has no plan yet at %s", dep.task, dep.plan)}
	}

	dep.planned = true

	return nil
}

// planIf is path when a plan is there, else "".
func planIf(path string) string {
	if !exists(path) {
		return ""
	}

	return path
}

// recordDependency writes the dependency under the branch's section of the
// local git config: per-branch, shared by every worktree, never pushed.
func recordDependency(ctx context.Context, dir, branch string, dep *dependency) error {
	section := "branch." + branch + "."

	if err := gitx.SetConfig(ctx, dir, section+cfgAfter, dep.task); err != nil {
		return fmt.Errorf("recording the dependency on %s: %w", dep.task, err)
	}

	if err := gitx.SetConfig(ctx, dir, section+cfgAfterBranch, dep.branch); err != nil {
		return fmt.Errorf("recording the dependency on %s: %w", dep.task, err)
	}

	return recordTip(ctx, dir, branch, dep.branch)
}

// recordTip records the base commit branch is built on, so a catchup after
// the base's history is rewritten replays only branch's own commits. The
// newer of the local and origin merge-bases wins: a guard integrates origin's
// copy, which a stale local branch would hide. A base found nowhere keeps the
// tip recorded before.
func recordTip(ctx context.Context, dir, branch, base string) error {
	var tip string

	for _, ref := range []string{"refs/heads/" + base, "refs/remotes/origin/" + base} {
		sha, err := gitx.MergeBase(ctx, dir, "refs/heads/"+branch, ref)

		switch {
		case gitx.UnknownObject(err):
			continue
		case err != nil:
			return err
		case sha == "" || sha == tip:
			continue
		}

		if tip != "" {
			older, err := gitx.IsAncestor(ctx, dir, sha, tip)
			if err != nil {
				return err
			}

			if older {
				continue
			}
		}

		tip = sha
	}

	if tip == "" {
		return nil
	}

	if err := gitx.SetConfig(ctx, dir, "branch."+branch+"."+cfgTip, tip); err != nil {
		return fmt.Errorf("recording %s's base tip on %s: %w", branch, base, err)
	}

	return nil
}
