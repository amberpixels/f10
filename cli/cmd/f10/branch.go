package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/driver"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// A task's branch is named and found in one file so the name start gives
// and the patterns finish and --after search with cannot drift apart.

// branchName is the task's branch name and, when no slug came, the note
// saying why.
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

// slugBranch is `<ID>/<slug>`: the id in front keeps the branch searchable
// by task, the slug says what it is where only the name shows. A bundle
// puts every id in front so trackers linking by issue key link each member.
func slugBranch(ctx context.Context, task ref.Ref, title func(context.Context) (string, error)) (string, string) {
	ids := task.Label()

	if title == nil {
		return ids, fmt.Sprintf("no title source for %s, so the branch is the bare id", task.ID)
	}

	text, err := title(ctx)
	if err != nil {
		return ids, fmt.Sprintf("no title for %s (%v), so the branch is the bare id", task.ID, err)
	}

	slug := slugify(text)
	if slug == "" {
		return ids, fmt.Sprintf("the title of %s yields no slug, so the branch is the bare id", task.ID)
	}

	return ids + "/" + slug, ""
}

// slugMax is where a slug is cut: long enough to carry a title's meaning,
// short enough for a tab label and a worktree path.
const slugMax = 40

// slugify cuts at the last hyphen at or before slugMax so no word is split,
// hard at slugMax when there is none.
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

// taskRefs are the patterns a task's branch can sit under a root. for-each-ref
// extends a bare id to everything below it at a slash, but a `*` stops at a
// slash, so a slugged bundle needs its own pattern.
func taskRefs(root, id string) []string {
	bundle := root + id + ref.Separator + "*"

	return []string{root + id, root + id + "-*", bundle, bundle + "/*"}
}

// riderRefs are the patterns of a bundle branch the id rides in, past its
// lead: `<lead>+<id>`, `<lead>+<id>+<more>`, each bare or slugged.
func riderRefs(root, id string) []string {
	mid := root + "*" + ref.Separator + id

	return []string{mid, mid + "/*", mid + ref.Separator + "*", mid + ref.Separator + "*/*"}
}

// branchBundle is the lead and riders a branch ships: its recorded link,
// else the ids its name spells, as a fetched branch carries them. A link git
// cannot read is an error: the name alone would read a bundle as its lead.
func branchBundle(ctx context.Context, dir, branch, prefix string) (string, []string, error) {
	section := "branch." + branch + "."

	recorded, err := gitx.Config(ctx, dir, section+cfgRiders)
	if err != nil {
		return "", nil, err
	}

	if riders := strings.Fields(recorded); len(riders) > 0 {
		lead, err := gitx.Config(ctx, dir, section+cfgTaskID)
		if err != nil {
			return "", nil, err
		}

		return cmp.Or(lead, ref.Match(prefix, branch)), riders, nil
	}

	if members := ref.Members(prefix, branch); members != nil {
		return members[0], members[1:], nil
	}

	return "", nil, nil
}

// branchesFor lists the task's branches under root, narrowed to a suffix
// when given. Locally, a miss on the shapes start creates falls back to the
// id anywhere in the name (`feat/ABC-6-slug`), as `task read` reads a branch.
func branchesFor(ctx context.Context, dir, root, id, suffix string) ([]string, error) {
	refs, err := gitx.Refs(ctx, dir, taskRefs(root, id)...)
	if err != nil {
		return nil, err
	}

	if len(refs) == 0 && root == "refs/heads/" {
		if refs, err = carryingBranches(ctx, dir, id); err != nil {
			return nil, err
		}
	}

	if suffix == "" {
		return refs, nil
	}

	return slices.DeleteFunc(refs, func(r string) bool { return !strings.HasSuffix(r, "-"+suffix) }), nil
}

// carryingBranches are the local branches carrying id past a prefix, minus
// any linked to another task, since a picked-up PR's branch may mention an id
// it does not ship. The prefix is the id's own, so GH-10 never reads as GH-1.
func carryingBranches(ctx context.Context, dir, id string) ([]string, error) {
	cut := strings.LastIndexByte(id, '-')
	if cut <= 0 {
		return nil, nil
	}

	all, err := gitx.Refs(ctx, dir, "refs/heads/")
	if err != nil {
		return nil, err
	}

	entries, err := gitx.ConfigEntries(ctx, dir, cfgTaskKeys)
	if err != nil {
		return nil, err
	}

	linked := map[string]string{}

	for _, e := range entries {
		if b, ok := strings.CutSuffix(e.Key, "."+cfgTaskID); ok {
			linked[strings.TrimPrefix(b, "branch.")] = e.Value
		}
	}

	match := ref.Matcher(id[:cut])

	return slices.DeleteFunc(all, func(b string) bool {
		if task, ok := linked[b]; ok && !strings.EqualFold(task, id) {
			return true
		}

		return !strings.EqualFold(match(b), id)
	}), nil
}

// taskBranch is the branch --after names. Nothing or several matching fails
// before anything is created, since guessing would base the work on the
// wrong attempt.
func taskBranch(ctx context.Context, dir, id string) (string, error) {
	linked, err := linkedBranches(ctx, dir, id)
	if err != nil {
		return "", err
	}

	switch len(linked) {
	case 0:
	case 1:
		return linked[0], nil
	default:
		return "", fmt.Errorf("--after %s: several branches are linked to it, name one: %s",
			id, strings.Join(linked, ", "))
	}

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

const cfgTaskKeys = `^branch\..*\.(` + cfgTaskID + `|` + cfgRiders + `)$`

// linkedBranches are the local branches linked to id as lead or rider: a
// picked-up PR's branch need not carry the id, and a bundle's branch is
// found by any member.
func linkedBranches(ctx context.Context, dir, id string) ([]string, error) {
	entries, err := gitx.ConfigEntries(ctx, dir, cfgTaskKeys)
	if err != nil {
		return nil, err
	}

	var names []string

	for _, e := range entries {
		branch, key := e.Key, ""
		for _, k := range []string{cfgTaskID, cfgRiders} {
			if b, ok := strings.CutSuffix(e.Key, "."+k); ok {
				branch, key = strings.TrimPrefix(b, "branch."), k
			}
		}

		ids := []string{e.Value}
		if key == cfgRiders {
			ids = strings.Fields(e.Value)
		}

		if slices.ContainsFunc(ids, func(s string) bool { return strings.EqualFold(s, id) }) &&
			!slices.Contains(names, branch) {
			names = append(names, branch)
		}
	}

	return names, nil
}

// namedRef is the ref --base names, with its commit. Exact refs rather than
// for-each-ref, since a base is one commit and a literal pattern would also
// match everything below the name at a slash.
func namedRef(ctx context.Context, dir, branch string) (string, string, error) {
	for _, root := range []string{"refs/heads/", "refs/remotes/", "refs/remotes/origin/"} {
		if sha := refSHA(ctx, dir, root+branch); sha != "" {
			return strings.TrimPrefix(strings.TrimPrefix(root+branch, "refs/heads/"), "refs/remotes/"), sha, nil
		}
	}

	return "", "", fmt.Errorf("--base %s: no branch %s locally or on origin", branch, branch)
}

func refSHA(ctx context.Context, dir, r string) string {
	return gitx.Out(ctx, dir, "rev-parse", "--verify", "--quiet", r)
}

// A foundBranch is the branch existingBranch settled on, and whether only
// origin has it, in which case start creates the local branch tracking it.
type foundBranch struct {
	name   string
	remote bool
}

// existingBranch is the branch to reuse, or "" when the task has none yet.
// Several is an error, since a suffix exists to say which; a suffix skips
// the link, naming an attempt by its branch name.
func existingBranch(ctx context.Context, dir, want, id, suffix string) (foundBranch, error) {
	if suffix == "" {
		linked, err := linkedBranches(ctx, dir, id)
		if err != nil {
			return foundBranch{}, err
		}

		switch len(linked) {
		case 0:
		case 1:
			return foundBranch{name: linked[0]}, nil
		default:
			return foundBranch{}, fmt.Errorf("several branches are linked to %s (git config branch.<name>.%s): %s",
				id, cfgTaskID, strings.Join(linked, ", "))
		}
	}

	for _, root := range []string{"refs/heads/", "refs/remotes/origin/"} {
		refs, err := branchesFor(ctx, dir, root, id, suffix)
		if err != nil {
			return foundBranch{}, err
		}

		for i, r := range refs {
			refs[i] = strings.TrimPrefix(r, "origin/")
		}

		remote := root != "refs/heads/"

		if slices.Contains(refs, want) {
			return foundBranch{name: want, remote: remote}, nil
		}

		switch len(refs) {
		case 0:
			continue
		case 1:
			return foundBranch{name: refs[0], remote: remote}, nil
		default:
			where := ""
			if remote {
				where = " on origin"
			}

			return foundBranch{}, fmt.Errorf("several branches for %s%s, add a suffix to pick one: %s",
				id, where, strings.Join(refs, ", "))
		}
	}

	return foundBranch{}, nil
}

// taskCheckout is the task's branch and the checkout that has it checked
// out: both "" with no local branch, the path "" when checked out nowhere.
// No title lookup: a slug would cost a host call for nothing.
func taskCheckout(
	ctx context.Context,
	main string,
	d *driver.Driver,
	task ref.Ref,
	suffix string,
) (string, string, error) {
	task = task.Head()

	want, _, err := branchName(ctx, d, task, suffix, nil)
	if err != nil {
		return "", "", err
	}

	found, err := existingBranch(ctx, main, want, task.ID, suffix)
	if err != nil || found.name == "" || found.remote {
		return "", "", err
	}

	name := found.name

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

// localDefault is the default branch as a local name. Without it finish
// cannot tell a stacked PR from one against main, nor what to pull.
func localDefault(ctx context.Context, dir string) (string, error) {
	def := defaultBranch(ctx, dir)
	if def == "" {
		return "", errors.New("cannot tell the default branch: origin has no HEAD here (git remote set-head origin -a)")
	}

	return strings.TrimPrefix(def, "origin/"), nil
}
