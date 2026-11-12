package stack

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/trackerdriver"
)

// BranchName is the task's branch name and, when no slug came, the note
// saying why.
func BranchName(
	ctx context.Context,
	d *trackerdriver.Driver,
	task ref.Ref,
	suffix string,
	title func(context.Context) (string, error),
) (string, string, error) {
	var name, note string

	if d != nil {
		out, err := d.Run(ctx, trackerdriver.VerbBranch, task.Number)

		switch {
		case err == nil:
			name, _, _ = strings.Cut(strings.TrimSpace(out), "\n")
			if name == "" {
				return "", "", errors.New("driver branch printed nothing")
			}
		case !errors.Is(err, trackerdriver.ErrUnsupported):
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

	slug := Slugify(text)
	if slug == "" {
		return ids, fmt.Sprintf("the title of %s yields no slug, so the branch is the bare id", task.ID)
	}

	return ids + "/" + slug, ""
}

// SlugMax is where a slug is cut: long enough to carry a title's meaning,
// short enough for a tab label and a worktree path.
const SlugMax = 40

// Slugify cuts at the last hyphen at or before SlugMax so no word is split,
// hard at SlugMax when there is none.
func Slugify(title string) string {
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
	if len(slug) <= SlugMax {
		return slug
	}

	if slug[SlugMax] == '-' {
		return slug[:SlugMax]
	}

	if i := strings.LastIndexByte(slug[:SlugMax], '-'); i > 0 {
		return slug[:i]
	}

	return slug[:SlugMax]
}

// TaskRefs are the patterns a task's branch can sit under a root. for-each-ref
// extends a bare id to everything below it at a slash, but a `*` stops at a
// slash, so a slugged bundle needs its own pattern.
func TaskRefs(root, id string) []string {
	bundle := root + id + ref.Separator + "*"

	return []string{root + id, root + id + "-*", bundle, bundle + "/*"}
}

// RiderRefs are the patterns of a bundle branch the id rides in, past its
// lead: `<lead>+<id>`, `<lead>+<id>+<more>`, each bare or slugged.
func RiderRefs(root, id string) []string {
	mid := root + "*" + ref.Separator + id

	return []string{mid, mid + "/*", mid + ref.Separator + "*", mid + ref.Separator + "*/*"}
}

// A BranchIndex answers which branch is a task's, from every branch's
// record read once. Build one per command and never keep it across a write
// to the records, since it answers from the read.
type BranchIndex struct {
	dir     string
	records []branchmeta.Record
}

func (x *BranchIndex) Record(branch string) branchmeta.Record {
	for _, r := range x.records {
		if r.Branch == branch {
			return r
		}
	}

	return branchmeta.Record{Branch: branch}
}

// Linked are the local branches linked to id as lead or rider: a picked-up
// PR's branch need not carry the id, and a bundle's branch is found by any
// member.
func (x *BranchIndex) Linked(id string) []string {
	var names []string

	same := func(s string) bool { return strings.EqualFold(s, id) }

	for _, r := range x.records {
		if same(r.Task) || slices.ContainsFunc(r.Riders, same) {
			names = append(names, r.Branch)
		}
	}

	return names
}

// Bundle is the lead and riders a branch ships: its recorded riders, led by
// its link else the id its name carries, then the ids its name spells, as a
// fetched branch carries them.
func (x *BranchIndex) Bundle(branch, prefix string) (string, []string) {
	r := x.Record(branch)
	if len(r.Riders) > 0 {
		return cmp.Or(r.Task, ref.Match(prefix, branch)), r.Riders
	}

	if members := ref.Members(prefix, branch); members != nil {
		return members[0], members[1:]
	}

	return "", nil
}

// Under lists the task's branches under root, narrowed to a suffix when
// given. Locally, a miss on the shapes start creates falls back to the id
// anywhere in the name (`feat/ABC-6-slug`), as `task read` reads a branch.
func (x *BranchIndex) Under(ctx context.Context, root, id, suffix string) ([]string, error) {
	refs, err := gitx.Refs(ctx, x.dir, TaskRefs(root, id)...)
	if err != nil {
		return nil, err
	}

	if len(refs) == 0 && root == "refs/heads/" {
		if refs, err = x.carrying(ctx, id); err != nil {
			return nil, err
		}
	}

	if suffix == "" {
		return refs, nil
	}

	return slices.DeleteFunc(refs, func(r string) bool { return !strings.HasSuffix(r, "-"+suffix) }), nil
}

// Find is the branch to reuse, or none when the task has none yet. Several
// is an error, since a suffix exists to say which; a suffix skips the link,
// naming an attempt by its branch name.
func (x *BranchIndex) Find(ctx context.Context, want, id, suffix string, roots BranchPolicy) (FoundBranch, error) {
	if suffix == "" {
		switch linked := x.Linked(id); len(linked) {
		case 0:
		case 1:
			return FoundBranch{Name: linked[0]}, nil
		default:
			return FoundBranch{}, fmt.Errorf("several branches are linked to %s (git config branch.<name>.%s): %s",
				id, branchmeta.Task, strings.Join(linked, ", "))
		}
	}

	for _, root := range roots {
		refs, err := x.Under(ctx, root, id, suffix)
		if err != nil {
			return FoundBranch{}, err
		}

		for i, r := range refs {
			refs[i] = strings.TrimPrefix(r, "origin/")
		}

		remote := root != "refs/heads/"

		if slices.Contains(refs, want) {
			return FoundBranch{Name: want, Remote: remote}, nil
		}

		switch len(refs) {
		case 0:
			continue
		case 1:
			return FoundBranch{Name: refs[0], Remote: remote}, nil
		default:
			where := ""
			if remote {
				where = " on origin"
			}

			return FoundBranch{}, fmt.Errorf("several branches for %s%s, add a suffix to pick one: %s",
				id, where, strings.Join(refs, ", "))
		}
	}

	return FoundBranch{}, nil
}

// After is the branch --after names. Nothing or several matching fails
// before anything is created, since guessing would base the work on the
// wrong attempt.
func (x *BranchIndex) After(ctx context.Context, id string) (string, error) {
	names, linked, err := x.candidates(ctx, id)

	switch {
	case err != nil:
		return "", err
	case len(names) == 1:
		return names[0], nil
	case len(names) == 0:
		return "", fmt.Errorf("--after %s: no branch for it locally or on origin", id)
	case linked:
		return "", fmt.Errorf("--after %s: several branches are linked to it, name one: %s",
			id, strings.Join(names, ", "))
	default:
		return "", fmt.Errorf("--after %s: several branches match, name one: %s", id, strings.Join(names, ", "))
	}
}

// Any is the first branch of the task, linked, local or origin's
// (`origin/<name>`), or "" for none.
func (x *BranchIndex) Any(ctx context.Context, id string) (string, error) {
	names, _, err := x.candidates(ctx, id)
	if err != nil || len(names) == 0 {
		return "", err
	}

	return names[0], nil
}

// carrying are the local branches carrying id past a prefix, minus any
// linked to another task, since a picked-up PR's branch may mention an id it
// does not ship. The prefix is the id's own, so GH-10 never reads as GH-1.
func (x *BranchIndex) carrying(ctx context.Context, id string) ([]string, error) {
	cut := strings.LastIndexByte(id, '-')
	if cut <= 0 {
		return nil, nil
	}

	all, err := gitx.Refs(ctx, x.dir, "refs/heads/")
	if err != nil {
		return nil, err
	}

	match := ref.Matcher(id[:cut])

	return slices.DeleteFunc(all, func(b string) bool {
		if task := x.Record(b).Task; task != "" && !strings.EqualFold(task, id) {
			return true
		}

		return !strings.EqualFold(match(b), id)
	}), nil
}

// candidates are the task's linked branches, else its branches under the
// first root of PolicyLocalOrOrigin that has any, and whether they are linked.
func (x *BranchIndex) candidates(ctx context.Context, id string) ([]string, bool, error) {
	if linked := x.Linked(id); len(linked) > 0 {
		return linked, true, nil
	}

	for _, root := range PolicyLocalOrOrigin {
		refs, err := x.Under(ctx, root, id, "")
		if err != nil || len(refs) > 0 {
			return refs, false, err
		}
	}

	return nil, false, nil
}

func LoadBranchIndex(ctx context.Context, dir string) (*BranchIndex, error) {
	records, err := branchmeta.LoadAll(ctx, dir)
	if err != nil {
		return nil, err
	}

	return &BranchIndex{dir: dir, records: records}, nil
}

// A BranchPolicy is where find looks past the link: the roots it searches,
// in order.
type BranchPolicy []string

var // PolicyLocalOrOrigin finds a branch only origin has, for start to track
PolicyLocalOrOrigin = BranchPolicy{"refs/heads/", "refs/remotes/origin/"}

// NamedRef is the ref --base names, with its commit. Exact refs rather than
// for-each-ref, since a base is one commit and a literal pattern would also
// match everything below the name at a slash.
func NamedRef(ctx context.Context, dir, branch string) (string, string, error) {
	for _, root := range []string{"refs/heads/", "refs/remotes/", "refs/remotes/origin/"} {
		if sha := RefSHA(ctx, dir, root+branch); sha != "" {
			return strings.TrimPrefix(strings.TrimPrefix(root+branch, "refs/heads/"), "refs/remotes/"), sha, nil
		}
	}

	return "", "", fmt.Errorf("--base %s: no branch %s locally or on origin", branch, branch)
}

func RefSHA(ctx context.Context, dir, r string) string {
	return gitx.Out(ctx, dir, "rev-parse", "--verify", "--quiet", r)
}

// A FoundBranch is the branch find settled on, and whether only
// origin has it, in which case start creates the local branch tracking it.
type FoundBranch struct {
	Name   string
	Remote bool
}

// TaskCheckout is the task's branch and the checkout that has it checked
// out: both "" with no local branch, the path "" when checked out nowhere.
// No title lookup: a slug would cost a host call for nothing.
func TaskCheckout(
	ctx context.Context,
	main string,
	d *trackerdriver.Driver,
	task ref.Ref,
	suffix string,
) (string, string, error) {
	task = task.Head()

	want, _, err := BranchName(ctx, d, task, suffix, nil)
	if err != nil {
		return "", "", err
	}

	x, err := LoadBranchIndex(ctx, main)
	if err != nil {
		return "", "", err
	}

	found, err := x.Find(ctx, want, task.ID, suffix, PolicyLocalOrOrigin)
	if err != nil || found.Name == "" || found.Remote {
		return "", "", err
	}

	name := found.Name

	wts, err := gitx.Worktrees(ctx, main)
	if err != nil {
		return "", "", err
	}

	return name, WorktreeOf(wts, name), nil
}

// DefaultBranch is what origin points HEAD at (`origin/main`), or "" to
// let git use the current HEAD - the same default worktrunk applies.
func DefaultBranch(ctx context.Context, dir string) string {
	return gitx.Out(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
}

// LocalDefault is the default branch as a local name. Without it finish
// cannot tell a stacked PR from one against main, nor what to pull.
func LocalDefault(ctx context.Context, dir string) (string, error) {
	def := DefaultBranch(ctx, dir)
	if def == "" {
		return "", errors.New("cannot tell the default branch: origin has no HEAD here (git remote set-head origin -a)")
	}

	return strings.TrimPrefix(def, "origin/"), nil
}
