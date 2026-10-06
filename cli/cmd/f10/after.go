package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// A dependency before git: a task that has not started has no branch and
// no git config to read, so the base it waits for is a fixed line in its
// body, `After: GH-12`, the same word as start's flag. Capture writes it,
// fetch reads it, start takes it as the default --after and drive checks it
// against the list before prompting anything. Git config stays the
// per-branch copy once the branch exists.

var afterLineRE = regexp.MustCompile(`(?m)^After:[ \t]*(\S+)[ \t]*\r?$`)

// afterIn is the id the body's After: line names, or "".
func afterIn(body string) string {
	m := afterLineRE.FindStringSubmatch(body)
	if m == nil {
		return ""
	}

	return m[1]
}

// A taskInfo is what the tracker says about a task beyond its branch.
type taskInfo struct {
	body   string
	closed bool
	pull   bool // the number is a pull request: GitHub numbers issues and PRs alike
}

// A tracker answers the questions start and drive ask of the task source.
// Funcs rather than a target, so the flows run under test without one.
type tracker struct {
	resolve func(ctx context.Context, token string) (ref.Ref, error)
	info    func(ctx context.Context, task ref.Ref) (taskInfo, error)
	archive []string // the dirs finish moves a finished task's plan into
}

// tracker binds the lookups to this target: the driver's `read` for the
// body where the project has one, the host's issue otherwise.
func (t *target) tracker() tracker {
	main := t.lay.MainRoot
	if main == "" {
		main = t.lay.CheckoutRoot
	}

	archive := filepath.Join(main, ".f10", "plans", "archive")
	if dir := t.sharedPlansDir(); dir != "" {
		archive = filepath.Join(dir, "archive")
	}

	return tracker{
		resolve: t.reference,
		info: func(ctx context.Context, task ref.Ref) (taskInfo, error) {
			if d := driver.Find(t.lay.StorageRoot, t.dir); d != nil {
				body, err := d.Run(ctx, driver.VerbRead, task.Number)
				if err == nil {
					return taskInfo{body: body}, nil
				}

				if !errors.Is(err, driver.ErrUnsupported) {
					return taskInfo{}, err
				}
			}

			h, err := t.host()
			if err != nil {
				return taskInfo{}, err
			}

			iss, err := h.issue(ctx, task.Number, "body,state,url")
			if err != nil {
				return taskInfo{}, err
			}

			url := iss.url()

			return taskInfo{
				body:   iss.body(),
				closed: strings.EqualFold(iss.State, "closed"),
				pull:   strings.Contains(url, "/pull/") || strings.Contains(url, "/merge_requests/"),
			}, nil
		},
		archive: []string{archive},
	}
}

// finishedTask reports whether a task is done with: no branch here, and the
// tracker closed it or finish archived its plan. The archive catches a merge
// whose PR body did not close the issue.
func finishedTask(info taskInfo, archive []string, id string, hasBranch bool) bool {
	if hasBranch {
		return false
	}

	if info.closed {
		return true
	}

	for _, dir := range archive {
		if exists(filepath.Join(dir, id+".md")) {
			return true
		}

		if older, _ := filepath.Glob(filepath.Join(dir, id+".*.md")); len(older) > 0 {
			return true
		}
	}

	return false
}

// hasTaskBranch reports whether the task has a branch, local or on origin.
func hasTaskBranch(ctx context.Context, dir, id string) (bool, error) {
	for _, root := range []string{"refs/heads/", "refs/remotes/origin/"} {
		refs, err := branchesFor(ctx, dir, root, id, "")
		if err != nil {
			return false, err
		}

		if len(refs) > 0 {
			return true, nil
		}
	}

	return false, nil
}

// bodyAfter turns the body's After: line into the base start uses when no
// flag names one. A base with a branch is the dependency; a finished base
// has its code in the default branch already; anything else would base the
// work on code that does not exist, and is refused before anything is made.
func bodyAfter(ctx context.Context, tr tracker, main string, task ref.Ref, body string) (string, string, error) {
	line := afterIn(body)
	if line == "" {
		return "", "", nil
	}

	base, err := tr.resolve(ctx, line)
	if err != nil {
		return "", "", fmt.Errorf("%s's body says After: %s: %w", task.ID, line, err)
	}

	branched, err := hasTaskBranch(ctx, main, base.ID)
	if err != nil {
		return "", "", err
	}

	if branched {
		return base.ID, fmt.Sprintf("after %s, from the task body's After: line", base.ID), nil
	}

	info, err := tr.info(ctx, base)
	if err != nil {
		return "", "", fmt.Errorf("%s's body says After: %s: %w", task.ID, base.ID, err)
	}

	if finishedTask(info, tr.archive, base.ID, false) {
		return "", fmt.Sprintf("After: %s is finished, so the branch is based on the default branch", base.ID), nil
	}

	return "", "", fmt.Errorf("%s's body says After: %s, which has no branch and is not finished: "+
		"start %s first, or pass --base", task.ID, base.ID, base.ID)
}
