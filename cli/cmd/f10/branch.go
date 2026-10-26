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

// A task's branch: how it is named when start creates it, and how it is
// found again by start, finish and --after once it exists, whether start
// named it or the id sits past a prefix. Both halves live here so the name
// start would give and the patterns finish searches with cannot drift apart.

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
// only the branch name shows - a Herdr tab, a worktree listing. A bundle
// puts every id in front, `ABC-1+ABC-2/<slug>`, the slug from the lead's
// title, so trackers that link branches by issue key link every member. No
// title, or one that slugs to nothing, leaves the bare ids and says so.
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
// the id with a dash suffix, and a bundle the id leads, bare or slugged
// (a for-each-ref `*` stops at a slash).
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
// else the ids its name spells, as a branch fetched from origin carries
// them. A branch that ships no bundle has no riders.
func branchBundle(ctx context.Context, dir, branch, prefix string) (string, []string) {
	section := "branch." + branch + "."

	if riders := strings.Fields(gitx.Out(ctx, dir, "config", "--get", section+cfgRiders)); len(riders) > 0 {
		return cmp.Or(gitx.Out(ctx, dir, "config", "--get", section+cfgTaskID), ref.Match(prefix, branch)), riders
	}

	if members := ref.Members(prefix, branch); members != nil {
		return members[0], members[1:]
	}

	return "", nil
}

// branchesFor lists the branches under root that belong to task id,
// narrowed to one suffix when the caller salted the name. Locally, a miss
// on the shapes start creates falls back to the id anywhere in the name
// (`feat/ABC-6-slug`), read by the rule `task read` applies to a branch.
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

// carryingBranches are the local branches whose name carries id past a
// prefix, minus any linked to another task: a picked-up PR's branch may
// mention an id it does not ship. The prefix is the id's own, so an id
// without one finds none, and GH-10 never reads as GH-1.
func carryingBranches(ctx context.Context, dir, id string) ([]string, error) {
	cut := strings.LastIndexByte(id, '-')
	if cut <= 0 {
		return nil, nil
	}

	all, err := gitx.Refs(ctx, dir, "refs/heads/")
	if err != nil {
		return nil, err
	}

	linked := map[string]string{}

	for _, e := range gitx.ConfigEntries(ctx, dir, cfgTaskKeys) {
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

// taskBranch is the branch --after names: the branch linked to the task,
// else its local branch, else its branch on origin. Nothing matching is a
// failure before anything is created; several matching is one too, since
// guessing would base the work on the wrong attempt.
func taskBranch(ctx context.Context, dir, id string) (string, error) {
	switch linked := linkedBranches(ctx, dir, id); len(linked) {
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

// cfgTaskKeys matches every branch's f10-task and f10-tasks entry in the
// local config.
const cfgTaskKeys = `^branch\..*\.(` + cfgTaskID + `|` + cfgRiders + `)$`

// linkedBranches are the local branches whose f10-task names id, or whose
// f10-tasks lists it: a branch picked up from a PR, whose name need not
// carry the id at all, and a bundle's branch, found by any member.
func linkedBranches(ctx context.Context, dir, id string) []string {
	var names []string

	for _, e := range gitx.ConfigEntries(ctx, dir, cfgTaskKeys) {
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

	return names
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

// A foundBranch is the branch existingBranch settled on, and whether only
// origin has it, in which case start creates the local branch tracking it.
type foundBranch struct {
	name   string
	remote bool
}

// existingBranch is the branch to reuse, or "" when the task has none yet.
// A branch linked to the task wins, then the name start would create, then
// the one task branch, locally and then on origin. Several is an error,
// since a suffix exists to say which. A suffix skips the link: it names an
// attempt by its branch name.
func existingBranch(ctx context.Context, dir, want, id, suffix string) (foundBranch, error) {
	if suffix == "" {
		switch linked := linkedBranches(ctx, dir, id); len(linked) {
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

// taskCheckout is where a task is being worked on: its branch, found the way
// start would find it, and the checkout that has the branch checked out.
// Both are "" when the task has no local branch; the path alone is "" when the
// branch is checked out nowhere. The checkout can be the main one, for a
// task shipped from main without a worktree. No title lookup: the branch exists or it
// does not, and a slug would cost a host call for nothing.
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
