package reviews

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/amberpixels/f10/cli/internal/archive"
	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/taskid"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// OpenReviews is the findings directory review round and lore change both
// read, and what the branch says about its series. A branch checked out
// elsewhere, or a subject that does not resolve, gets no series read, so a
// closed one is archived only where its branch lives.
func OpenReviews(ctx context.Context, t *target.Target, explicit string, warn io.Writer) (string, roundSeries, error) {
	key, err := reviewsKey(ctx, t, explicit)
	if err != nil {
		return "", roundSeries{}, err
	}

	dir := t.Layout.StorageRoot.Reviews(key)

	s, err := ResolvePRSubject(ctx, t, explicit)
	if err != nil || !s.Here {
		return dir, roundSeries{}, nil //nolint:nilerr // the rounds the directory holds still answer
	}

	series, err := seriesOf(ctx, t, s, dir, warn)

	return dir, series, err
}

// reviewsKey names a task's findings directory: the task id (a bundle's
// lead), else the branch with `/` as `-` for work no task names.
func reviewsKey(ctx context.Context, t *target.Target, explicit string) (string, error) {
	r, err := t.Reference(ctx, explicit)
	if err == nil {
		return r.Head().ID, nil
	}

	if !errors.Is(err, ref.ErrNoReference) {
		return "", err
	}

	branch := gitx.CurrentBranch(ctx, t.Dir)
	if branch == "" {
		return "", fmt.Errorf("%w, and no branch is checked out to name the findings directory", err)
	}

	return strings.ReplaceAll(branch, "/", "-"), nil
}

// A roundSeries is what the branch's state says about its findings
// directory before the rounds are counted.
type roundSeries struct {
	Merged   bool   // main already holds the branch's content
	Pr       string // the open PR for the next header, `none` without one
	Archived string // where a finished series moved
}

// seriesOf checks for a merged branch first, so a branch left over after its
// PR merged declines instead of archiving rounds and opening a new series.
func seriesOf(ctx context.Context, t *target.Target, s prSubject, dir string, warn io.Writer) (roundSeries, error) {
	series := roundSeries{Pr: "none"}

	h, hostErr := hostops.HostOf(t)
	if s.Number > 0 {
		series.Pr = "#" + strconv.Itoa(s.Number)
		if hostErr == nil {
			series.Pr = h.Sigil() + strconv.Itoa(s.Number)
		}
	}

	base, err := s.Base(ctx, io.Discard)
	if err == nil {
		series.Merged = alreadyMerged(ctx, s.Path, base)
	}

	if series.Merged {
		return series, nil
	}

	latest := findings.Latest(dir)
	if latest == "" {
		return series, nil
	}

	raw, err := os.ReadFile(latest)
	if err != nil {
		return series, err
	}

	f, err := findings.Parse(string(raw))
	if err != nil {
		return series, nil //nolint:nilerr // findings.Rounds names the malformed file
	}

	if !seriesClosed(ctx, s.Path, f, h, hostErr, warn) {
		return series, nil
	}

	head, _, _ := strings.Cut(f.Reviewed, " ")
	restart := !taskid.IsNumber(strings.TrimLeft(f.PR, "#!"))
	series.Archived, err = archive.ArchiveReviews(
		dir,
		archive.SeriesEnd{Pr: cmp.Or(f.PR, "none"), Head: head, Restart: restart},
	)

	return series, err
}

// alreadyMerged reports whether merging the tree's work into base changes
// nothing: tree equality, not ancestry, since a squash merge leaves the
// branch's commits off main. A branch with nothing past base yet, such as
// the coherence lens's fresh sweep branch, is not merged, and neither are
// untracked work or a branch that does not merge.
func alreadyMerged(ctx context.Context, dir, base string) bool {
	if untracked, _ := gitx.Exec(ctx, dir, "ls-files", "--others", "--exclude-standard"); untracked != "" {
		return false
	}

	head, err := CapturedHead(ctx, dir)
	if err != nil {
		return false
	}

	into, tree, err := MergedTree(ctx, dir, head, base)
	if err != nil {
		return false
	}

	if fresh, err := gitx.IsAncestor(ctx, dir, head, into); err != nil || fresh {
		return false
	}

	own, err := gitx.Exec(ctx, dir, "rev-parse", into+"^{tree}")

	return err == nil && own == tree
}

// seriesClosed reads the round's PR on the host; a lookup that fails keeps
// the series, since a wrongly closed one archives findings resolve still
// needs. A round naming no PR ends when HEAD cannot reach its reviewed sha,
// and one recording neither stays.
func seriesClosed(
	ctx context.Context,
	dir string,
	f *findings.File,
	h hostops.Host,
	hostErr error,
	warn io.Writer,
) bool {
	if n := strings.TrimLeft(f.PR, "#!"); taskid.IsNumber(n) {
		if hostErr != nil {
			ui.Warnf(warn, "cannot read %s (%v), so its series of rounds goes on", f.PR, hostErr)

			return false
		}

		p, err := h.PR(ctx, n)
		if err != nil {
			ui.Warnf(warn, "cannot read %s (%v), so its series of rounds goes on", f.PR, err)

			return false
		}

		return p.Status == forge.Merged || p.Status == forge.Closed
	}

	sha, _, _ := strings.Cut(f.Reviewed, " ")
	if sha == "" {
		return false
	}

	reachable, err := gitx.IsAncestor(ctx, dir, sha, "HEAD")

	return err == nil && !reachable
}
