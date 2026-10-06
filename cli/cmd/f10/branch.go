package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// A task's branch: how it is named when start creates it, and how it is
// found again by start, finish and --after once it exists. Both halves
// live here so the name start would give and the patterns finish searches
// with cannot drift apart.

// branchName is the driver's answer to `branch`, else `<ID>/<slug>` from
// the task's title, else the bare id with a note saying why no slug came -
// and the suffix appended to whichever came back.
func branchName(
	ctx context.Context,
	d *driver.Driver,
	task ref.Ref,
	suffix string,
	title func(context.Context) (string, error),
) (string, string, error) {
	var name, note string

	if d != nil {
		out, err := d.Run(ctx, driver.VerbBranch, task.Number)

		switch {
		case err == nil:
			name, _, _ = strings.Cut(strings.TrimSpace(out), "\n")
			if name == "" {
				return "", "", errors.New("driver branch printed nothing")
			}
		case !errors.Is(err, driver.ErrUnsupported):
			return "", "", err
		}
	}

	if name == "" {
		name, note = slugBranch(ctx, task, title)
	}

	if suffix != "" {
		name += "-" + suffix
	}

	return name, note, nil
}

// slugBranch is the default shape, `<ID>/<slug>`: the id in front keeps
// the branch searchable by its task, the slug says what the task is where
// only the branch name shows - a Herdr tab, a worktree listing. No title,
// or one that slugs to nothing, leaves the bare id and says so.
func slugBranch(ctx context.Context, task ref.Ref, title func(context.Context) (string, error)) (string, string) {
	if title == nil {
		return task.ID, fmt.Sprintf("no title source for %s, so the branch is the bare id", task.ID)
	}

	text, err := title(ctx)
	if err != nil {
		return task.ID, fmt.Sprintf("no title for %s (%v), so the branch is the bare id", task.ID, err)
	}

	slug := slugify(text)
	if slug == "" {
		return task.ID, fmt.Sprintf("the title of %s yields no slug, so the branch is the bare id", task.ID)
	}

	return task.ID + "/" + slug, ""
}

// slugMax is where a slug is cut: long enough to carry a title's meaning,
// short enough for a tab label and a worktree path.
const slugMax = 40

// slugify turns a title into a branch segment: ASCII letters and digits
// kept and lowercased, every other run of characters a single hyphen, cut
// at the last hyphen at or before slugMax so no word is split, hard at
// slugMax when there is none.
func slugify(title string) string {
	var (
		b   strings.Builder
		gap bool
	)

	for _, r := range strings.ToLower(title) {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			gap = true

			continue
		}

		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}

		gap = false

		b.WriteRune(r)
	}

	slug := b.String()
	if len(slug) <= slugMax {
		return slug
	}

	if slug[slugMax] == '-' {
		return slug[:slugMax]
	}

	if i := strings.LastIndexByte(slug[:slugMax], '-'); i > 0 {
		return slug[:i]
	}

	return slug[:slugMax]
}

// taskRefs are the patterns a task's branch can sit under a root: the id
// itself (which for-each-ref extends to everything below it at a slash),
// and the id with a dash suffix.
func taskRefs(root, id string) []string {
	return []string{root + id, root + id + "-*"}
}

// branchesFor lists the branches under root that belong to task id,
// narrowed to one suffix when the caller salted the name.
func branchesFor(ctx context.Context, dir, root, id, suffix string) ([]string, error) {
	refs, err := gitx.Refs(ctx, dir, taskRefs(root, id)...)
	if err != nil {
		return nil, err
	}

	if suffix == "" {
		return refs, nil
	}

	return slices.DeleteFunc(refs, func(r string) bool { return !strings.HasSuffix(r, "-"+suffix) }), nil
}

// taskBranch is the branch --after names: the task's local branch, else its
// branch on origin. Nothing matching is a failure before anything is
// created; several matching is one too, since guessing would base the work
// on the wrong attempt.
func taskBranch(ctx context.Context, dir, id string) (string, error) {
	for _, root := range []string{"refs/heads/", "refs/remotes/origin/"} {
		refs, err := branchesFor(ctx, dir, root, id, "")
		if err != nil {
			return "", err
		}

		switch len(refs) {
		case 0:
			continue
		case 1:
			return refs[0], nil
		default:
			return "", fmt.Errorf("--after %s: several branches match, name one: %s", id, strings.Join(refs, ", "))
		}
	}

	return "", fmt.Errorf("--after %s: no branch for it locally or on origin", id)
}

// namedRef is the ref --base names, with the commit it points at: the local
// branch, else a remote branch typed as such (`origin/main`), else the
// branch on origin. Exact refs rather than for-each-ref, since a base is
// one commit and a literal pattern would also match everything below the
// name at a slash. Nothing matching is a failure before anything is created.
func namedRef(ctx context.Context, dir, branch string) (string, string, error) {
	for _, root := range []string{"refs/heads/", "refs/remotes/", "refs/remotes/origin/"} {
		if sha := refSHA(ctx, dir, root+branch); sha != "" {
			return strings.TrimPrefix(strings.TrimPrefix(root+branch, "refs/heads/"), "refs/remotes/"), sha, nil
		}
	}

	return "", "", fmt.Errorf("--base %s: no branch %s locally or on origin", branch, branch)
}

// refSHA is the commit a ref points at - a full ref, or a branch name git
// expands itself - or "" when there is no such ref.
func refSHA(ctx context.Context, dir, r string) string {
	return gitx.Out(ctx, dir, "rev-parse", "--verify", "--quiet", r)
}

// existingBranch is the branch to reuse, or "" when the task has none yet.
// The name start would create wins outright; otherwise one task branch is
// reused, and several is an error, since a suffix exists to say which.
func existingBranch(ctx context.Context, dir, want, id, suffix string) (string, error) {
	refs, err := branchesFor(ctx, dir, "refs/heads/", id, suffix)
	if err != nil {
		return "", err
	}

	if slices.Contains(refs, want) {
		return want, nil
	}

	switch len(refs) {
	case 0:
		return "", nil
	case 1:
		return refs[0], nil
	default:
		return "", fmt.Errorf("several branches for %s, add a suffix to pick one: %s", id, strings.Join(refs, ", "))
	}
}

// taskCheckout is where a task is being worked on: its branch, found the way
// start would find it, and the checkout that has the branch checked out.
// Both are "" when the task has no branch; the path alone is "" when the
// branch is checked out nowhere. No title lookup: the branch exists or it
// does not, and a slug would cost a host call for nothing.
func taskCheckout(
	ctx context.Context,
	main string,
	d *driver.Driver,
	task ref.Ref,
	suffix string,
) (string, string, error) {
	want, _, err := branchName(ctx, d, task, suffix, nil)
	if err != nil {
		return "", "", err
	}

	name, err := existingBranch(ctx, main, want, task.ID, suffix)
	if err != nil || name == "" {
		return "", "", err
	}

	wts, err := gitx.Worktrees(ctx, main)
	if err != nil {
		return "", "", err
	}

	return name, worktreeOf(wts, name), nil
}

// defaultBranch is what origin points HEAD at (`origin/main`), or "" to
// let git use the current HEAD - the same default worktrunk applies.
func defaultBranch(ctx context.Context, dir string) string {
	return gitx.Out(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
}

// localDefault is the default branch as a local name ("main"), read from
// what origin points HEAD at. Without it finish cannot tell a stacked PR
// from one against main, nor what to pull.
func localDefault(ctx context.Context, dir string) (string, error) {
	def := defaultBranch(ctx, dir)
	if def == "" {
		return "", errors.New("cannot tell the default branch: origin has no HEAD here (git remote set-head origin -a)")
	}

	return strings.TrimPrefix(def, "origin/"), nil
}
