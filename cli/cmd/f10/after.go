package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/mdline"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// A dependency before git: a task that has not started has no branch and
// no git config to read, so the base it waits for is a fixed line in its
// body, `After: GH-12`, the same word as start's flag. Capture writes it,
// fetch reads it, start takes it as the default --after and drive checks it
// against the list before prompting anything. Git config stays the
// per-branch copy once the branch exists.

// afterIn is the id the body's After: line names, or "". The line starts
// at column 0 and names one id, bare or as a link to the task
// (`After: [GH-12: Add the index](https://...)`); emphasis or code marks
// around either part (`**After:** GH-12`) do not change it.
func afterIn(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if id := afterID(line); id != "" {
			return id
		}
	}

	return ""
}

// afterID is the id one line names as an After: line, or "".
func afterID(line string) string {
	line = strings.TrimRight(line, "\r")
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return ""
	}

	key, value, ok := mdline.Cut(line, ":")
	if !ok || !strings.EqualFold(key, "after") {
		return ""
	}

	id := mdline.Bare(value)

	// a link's id is its text up to the title: the url is never read, so a
	// moved task still names its base. Nothing may follow the link, as
	// nothing may follow a bare id.
	if text, ok := strings.CutPrefix(id, "["); ok && strings.Contains(text, "](") {
		text, url, _ := strings.Cut(text, "](")
		if !strings.HasSuffix(url, ")") || strings.ContainsAny(url, " \t") {
			return ""
		}

		text, _, _ = strings.Cut(text, ":")
		id = strings.TrimSpace(text)
	}

	if id == "" || strings.ContainsAny(id, " \t[]") {
		return ""
	}

	return id
}

// linkAfter puts link in place of the value on body's first After: line.
func linkAfter(body, link string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if afterID(line) != "" {
			lines[i] = "After: " + link

			break
		}
	}

	return strings.Join(lines, "\n")
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
	// children is the task's open subtasks; nil, like errNoParentLink, is a
	// tracker with no parent link
	children func(ctx context.Context, task ref.Ref) ([]childRow, error)
	// allChildren is children with the closed ones kept, for status
	allChildren func(ctx context.Context, task ref.Ref) ([]childRow, error)
	archive     []string // the dirs finish moves a finished task's plan into
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

	tr := tracker{
		resolve: t.reference,
		info: func(ctx context.Context, task ref.Ref) (taskInfo, error) {
			if d := t.driver(); d != nil {
				body, err := d.Run(ctx, driver.VerbRead, task.Number)
				if err == nil {
					return taskInfo{body: body}, nil
				}

				if !errors.Is(err, driver.ErrUnsupported) {
					return taskInfo{}, err
				}
			}

			h, err := t.issues(driver.VerbRead)
			if err != nil {
				return taskInfo{}, err
			}

			iss, err := h.issue(ctx, task.Number, "body,state,url")
			if err != nil {
				return taskInfo{}, err
			}

			return taskInfo{
				body:   iss.body(),
				closed: strings.EqualFold(iss.State, "closed"),
				pull:   iss.pull(),
			}, nil
		},
		archive: []string{archive},
	}

	children := func(keepClosed bool) func(context.Context, ref.Ref) ([]childRow, error) {
		return func(ctx context.Context, task ref.Ref) ([]childRow, error) {
			raw, err := t.rawChildren(ctx, task)
			if err != nil {
				return nil, err
			}

			rows, err := shapeChildren(ctx, tr, main, raw, keepClosed)
			t.rememberRefs(ctx, childRefs(rows)...)

			return rows, err
		}
	}

	tr.children, tr.allChildren = children(false), children(true)

	return tr
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

// hasTaskBranch reports whether the task has a branch: linked to it, or
// named for it locally or on origin.
func hasTaskBranch(ctx context.Context, dir, id string) (bool, error) {
	if len(linkedBranches(ctx, dir, id)) > 0 {
		return true, nil
	}

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
	line, err := bundleAfter(ctx, tr, task, body)
	if err != nil || line == "" {
		return "", "", err
	}

	base, err := tr.resolve(ctx, line)
	if err != nil {
		return "", "", fmt.Errorf("%s's body says After: %s: %w", task.ID, line, err)
	}

	// a base that rides in a bundle is waited for as the bundle, by its lead
	base = base.Head()

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

// bundleAfter is the After: line a task answers to: its body's, or for a
// bundle, the one base its members' bodies name outside the bundle. A line
// naming another member orders work inside the one branch, so it is
// dropped; two different bases cannot both be the branch's.
func bundleAfter(ctx context.Context, tr tracker, task ref.Ref, body string) (string, error) {
	if len(task.Riders()) == 0 {
		return afterIn(body), nil
	}

	var line, base, from string

	for i, m := range task.MemberRefs() {
		text := body
		if i > 0 {
			info, err := tr.info(ctx, m)
			if err != nil {
				return "", fmt.Errorf("reading %s for its After: line: %w", m.ID, err)
			}

			text = info.body
		}

		l := afterIn(text)
		if l == "" {
			continue
		}

		r, err := tr.resolve(ctx, l)
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
