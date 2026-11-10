package standup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amberpixels/f10/cli/internal/branchmeta"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
)

// Source is the checkout a standup reads.
type Source struct {
	Dir    string      // the checkout
	Main   string      // the main checkout, where branch records live
	Prefix string      // the task id prefix
	Upkeep []string    // project.md's upkeep globs
	Host   *forge.Host // nil: no host CLI for this checkout
}

// Collect reads git and, where the checkout has one, the host. A checkout
// with no host CLI reports commits only; a host CLI that fails is the
// command's error, never a quiet fall back to commits.
func Collect(ctx context.Context, src Source, since time.Time) (Input, error) {
	email := gitx.Out(ctx, src.Dir, "config", "user.email")
	if email == "" {
		return Input{}, errors.New("git has no user.email here, and standup matches your commits by it " +
			"(git config user.email you@example.com)")
	}

	remotes := strings.Fields(gitx.Out(ctx, src.Dir, "remote"))

	defRef, def, err := defaultRef(ctx, src, remotes)
	if err != nil {
		return Input{}, err
	}

	commits, err := userCommits(ctx, src.Dir, email, since, defRef, def, remotes)
	if err != nil {
		return Input{}, err
	}

	bases := recordedBases(ctx, src.Main)
	commitFacts(ctx, src.Dir, commits, defRef, def, bases)

	in := Input{
		Prefix:        src.Prefix,
		Since:         since,
		Checkout:      src.Dir,
		DefaultBranch: def,
		User:          User{Email: email},
		Commits:       commits,
		Bases:         bases,
		UpkeepPaths:   src.Upkeep,
	}

	if src.Host == nil {
		return in, nil
	}

	login, err := src.Host.Login(ctx)
	if err != nil {
		return Input{}, err
	}

	pulls, err := src.Host.Pulls(ctx, login, since)
	if err != nil {
		return Input{}, err
	}

	prs := hostPRs(pulls)

	// a force-push's commits are taken against the PR's base: the base f10
	// recorded, else the host's
	for i, p := range prs {
		if len(p.ForcePushes) == 0 {
			continue
		}

		base := baseRefs(ctx, src.Dir, cmp.Or(bases[p.Head], p.Base), defRef, def)
		if len(base) == 0 {
			continue
		}

		for j, fp := range p.ForcePushes {
			prs[i].ForcePushes[j].SamePatches = samePatches(ctx, src.Dir, base[0], fp.Before, fp.After)
		}
	}

	// a parent the host never saw (an unpushed commit) has no CI: unknown,
	// which classifies as work rather than failing the report
	for i, c := range commits {
		if c.At.Before(since) || !NeedsCI(c, src.Upkeep) {
			continue
		}

		if ci, err := src.Host.CommitCI(ctx, c.Parents[0]); err == nil && ci != nil {
			commits[i].ParentCI = &CI{Red: ci.Red, At: ci.At}
		}
	}

	in.Host, in.User.Login, in.PRs = src.Host.Name, login, prs

	return in, nil
}

// hostPRs is forge's requests in this package's shape, the fields Group reads.
func hostPRs(pulls []forge.PR) []HostPR {
	out := make([]HostPR, 0, len(pulls))

	for _, p := range pulls {
		hp := HostPR{
			Number: p.Number, Title: p.Title, URL: p.URL, Status: p.Status, Draft: p.Draft, Empty: p.Empty,
			Head: p.Head, Base: p.Base, Author: p.Author, CreatedAt: p.CreatedAt, MergedAt: p.MergedAt,
			MergedBy: p.MergedBy, MergeSHA: p.MergeSHA,
		}

		for _, c := range p.Commits {
			hp.Commits = append(hp.Commits, PRCommit{SHA: c.SHA, Subject: c.Subject, At: c.At})
		}

		for _, r := range p.Reviews {
			hp.Reviews = append(hp.Reviews, Review{Author: r.Author, At: r.At})
		}

		for _, fp := range p.ForcePushes {
			hp.ForcePushes = append(hp.ForcePushes, ForcePush{At: fp.At, Before: fp.Before, After: fp.After})
		}

		out = append(out, hp)
	}

	return out
}

// defaultRef is the default branch as a ref and a name: origin/HEAD,
// else the host's answer. Without either, landed commits would read as
// unfinished work, so it refuses; a checkout with no remote has none.
func defaultRef(ctx context.Context, src Source, remotes []string) (string, string, error) {
	if r := gitx.Out(ctx, src.Dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); r != "" {
		return r, refBranch(r, remotes, ""), nil
	}

	if len(remotes) == 0 {
		return "", "", nil
	}

	if src.Host == nil {
		return "", "", errors.New(
			"cannot tell the default branch: origin has no HEAD here (git remote set-head origin -a)",
		)
	}

	name, err := src.Host.DefaultBranchName(ctx)
	if err != nil {
		return "", "", err
	}

	for _, r := range []string{"refs/remotes/origin/" + name, "refs/heads/" + name} {
		if gitx.Out(ctx, src.Dir, "rev-parse", "--verify", "--quiet", r) != "" {
			return r, name, nil
		}
	}

	return "", name, nil
}

// logFormat is one commit per line, fields split by the unit separator,
// the subject last: it is the only field that can hold anything.
const logFormat = "--format=%H%x1f%aE%x1f%aI%x1f%P%x1f%S%x1f%s"

// userCommits is the user's commits on any branch, local or remote-tracking,
// by mailmapped author email: --author would match the name too. --since
// only narrows the walk by committer date; Group keeps commits by author date.
func userCommits(
	ctx context.Context, dir, email string, since time.Time, defRef, def string, remotes []string,
) ([]Commit, error) {
	stamp := "--since=" + since.Format(time.RFC3339)

	out, err := gitx.Exec(ctx, dir, "log", "--branches", "--remotes", "--source", stamp, logFormat)
	if err != nil {
		return nil, err
	}

	// --source names the first ref the walk reached a commit from, which
	// for a fresh task branch is the branch, not the default it came from
	onDefault := map[string]bool{}

	if defRef != "" {
		for sha := range strings.FieldsSeq(gitx.Out(ctx, dir, "log", "--format=%H", stamp, defRef)) {
			onDefault[sha] = true
		}
	}

	var commits []Commit

	for line := range strings.SplitSeq(out, "\n") {
		f := strings.SplitN(line, "\x1f", 6)
		if len(f) < 6 || !strings.EqualFold(f[1], email) {
			continue
		}

		at, err := time.Parse(time.RFC3339, f[2])
		if err != nil {
			return nil, fmt.Errorf("git log: author date %q: %w", f[2], err)
		}

		branch := refBranch(f[4], remotes, def)
		if onDefault[f[0]] {
			branch = def
		}

		commits = append(commits, Commit{
			SHA: f[0], Subject: f[5], At: at, Branch: branch, Parents: strings.Fields(f[3]),
		})
	}

	return commits, nil
}

// numstatFormat opens each commit's numstat block with its sha.
const numstatFormat = "--format=%x1e%H"

// generatedHeader is Go's marker for a generated file (go help generate).
const generatedHeader = `^// Code generated .* DO NOT EDIT\.$`

// commitFacts fills in what the upkeep rules read from git, one walk or
// call for all commits; a read that fails leaves the fact unset, which
// classifies as work.
func commitFacts(
	ctx context.Context,
	dir string,
	commits []Commit,
	defRef, def string,
	bases map[string]string,
) {
	var single []string

	for _, c := range commits {
		if len(c.Parents) == 1 {
			single = append(single, c.SHA)
		}
	}

	files := changedFiles(ctx, dir, single)

	var paths, goPaths, shas []string

	for i := range commits {
		c := &commits[i]
		c.Files = files[c.SHA]

		for _, f := range c.Files {
			paths = append(paths, f.Path)

			if strings.HasSuffix(f.Path, ".go") {
				goPaths = append(goPaths, f.Path)
			}
		}

		if len(c.Files) > 0 {
			shas = append(shas, c.SHA)
		}
	}

	generated := generatedPaths(ctx, dir, paths, shas, goPaths)

	for i := range commits {
		c := &commits[i]
		for j, f := range c.Files {
			c.Files[j].Generated = generated[f.Path] || generated[c.SHA+":"+f.Path]
		}

		// a merge made on the default branch merges into it; one not yet
		// pushed would otherwise read as merging origin's tip in
		if len(c.Parents) < 2 || c.Branch == def && (defRef == "" || !onDefault(ctx, dir, c.SHA, defRef)) {
			continue
		}

		for _, base := range baseRefs(ctx, dir, bases[c.Branch], defRef, "") {
			if onFirstParentLine(ctx, dir, base, c.Parents[1]) {
				c.BaseMerge = true

				break
			}
		}
	}
}

// changedFiles is the numstat of the given non-merge commits, by sha: the
// user's own only, so the cost follows their activity, not the team's.
// --no-renames lists a rename as a delete and an add, so both paths count.
func changedFiles(ctx context.Context, dir string, shas []string) map[string][]File {
	if len(shas) == 0 {
		return nil
	}

	out := gitx.Out(ctx, dir, append([]string{"show", "--no-renames", "--numstat", numstatFormat}, shas...)...)

	files := map[string][]File{}

	for block := range strings.SplitSeq(out, "\x1e") {
		sha, rest, _ := strings.Cut(strings.TrimSpace(block), "\n")
		if sha == "" {
			continue
		}

		for line := range strings.SplitSeq(rest, "\n") {
			f := strings.SplitN(line, "\t", 3)
			if len(f) < 3 {
				continue
			}

			added, errA := strconv.Atoi(f[0])
			deleted, errD := strconv.Atoi(f[1])
			files[sha] = append(files[sha], File{
				Path: f[2], Added: added, Deleted: deleted, Binary: errA != nil || errD != nil,
			})
		}
	}

	return files
}

// generatedPaths is the paths marked linguist-generated in the working
// tree's attributes, keyed by path, and the Go files carrying the
// generated header at a commit, keyed by sha:path.
func generatedPaths(ctx context.Context, dir string, paths, shas, goPaths []string) map[string]bool {
	out := map[string]bool{}

	if paths = sortedUnique(paths); len(paths) > 0 {
		attrs := gitx.Out(ctx, dir, append([]string{"check-attr", "linguist-generated", "--"}, paths...)...)

		for line := range strings.SplitSeq(attrs, "\n") {
			p, value, ok := strings.Cut(line, ": linguist-generated: ")
			if ok && (value == "set" || value == "true") {
				out[p] = true
			}
		}
	}

	if goPaths = sortedUnique(goPaths); len(goPaths) > 0 {
		args := append([]string{"grep", "-l", "-E", generatedHeader}, shas...)
		args = append(append(args, "--"), goPaths...)

		for line := range strings.SplitSeq(gitx.Out(ctx, dir, args...), "\n") {
			if line != "" {
				out[line] = true
			}
		}
	}

	return out
}

// baseRefs is the refs a branch's base can be read from: the recorded
// base, remote-tracking first, then the default branch's ref.
func baseRefs(ctx context.Context, dir, base, defRef, def string) []string {
	var refs []string

	if base != "" && base != def {
		for _, r := range []string{"refs/remotes/origin/" + base, "refs/heads/" + base} {
			if gitx.Out(ctx, dir, "rev-parse", "--verify", "--quiet", r) != "" {
				refs = append(refs, r)

				break
			}
		}
	}

	if defRef != "" {
		refs = append(refs, defRef)
	}

	return refs
}

// onFirstParentLine reports whether commit is on base's first-parent line.
// Plain ancestry would also count a feature branch that has since landed.
func onFirstParentLine(ctx context.Context, dir, base, commit string) bool {
	out, err := gitx.Exec(ctx, dir, "rev-list", "--first-parent", "--parents", base, "--not", commit)
	if err != nil {
		return false
	}

	if out == "" {
		return gitx.Out(ctx, dir, "rev-parse", "--verify", "--quiet", base+"^{commit}") == commit
	}

	lines := strings.Split(out, "\n")
	f := strings.Fields(lines[len(lines)-1])

	return len(f) > 1 && f[1] == commit
}

// rangeDiffPair is one line of range-diff's pairing: the old and new
// commit and how they compare (= same patch, ! changed, < dropped, > added).
var rangeDiffPair = regexp.MustCompile(`^\s*(?:\d+|-):\s+\S+\s+([=!<>])\s+(?:\d+|-):`)

// samePatches is true when every commit a force-push replaced kept its patch
// against base, nil when git cannot tell, typically because the old commits
// were never fetched here.
func samePatches(ctx context.Context, dir, base, before, after string) *bool {
	if before == "" || after == "" {
		return nil
	}

	out, err := gitx.Exec(ctx, dir, "range-diff", "--no-color", base, before, after)
	if err != nil {
		return nil
	}

	pairs, same := 0, true

	for line := range strings.SplitSeq(out, "\n") {
		m := rangeDiffPair.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		pairs++
		same = same && m[1] == "="
	}

	if pairs == 0 {
		return nil
	}

	return &same
}

func sortedUnique(s []string) []string {
	slices.Sort(s)

	return slices.Compact(s)
}

// refBranch is a ref as the branch it stands for: refs/ and a remote's
// prefix dropped, so origin/x and x are one branch, and a remote's HEAD is
// the default branch.
func refBranch(r string, remotes []string, def string) string {
	r = strings.TrimPrefix(strings.TrimPrefix(r, "refs/heads/"), "refs/remotes/")

	for _, remote := range remotes {
		if rest, ok := strings.CutPrefix(r, remote+"/"); ok {
			r = rest

			break
		}
	}

	if r == "HEAD" {
		return def
	}

	return r
}

// recordedBases is every head branch's base as `f10 start --after` wrote
// it: the real base of a stacked PR after the base's finish retargets it.
func recordedBases(ctx context.Context, main string) map[string]string {
	out := map[string]string{}

	// display only: an unreadable config records no base
	records, _ := branchmeta.LoadAll(ctx, main)
	for _, r := range records {
		if r.AfterBranch != "" {
			out[r.Branch] = r.AfterBranch
		}
	}

	return out
}

// onDefault is display only: a commit git cannot place counts as not on it.
func onDefault(ctx context.Context, dir, sha, defRef string) bool {
	ok, _ := gitx.IsAncestor(ctx, dir, sha, defRef)

	return ok
}
