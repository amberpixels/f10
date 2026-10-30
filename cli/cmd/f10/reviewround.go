package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/findings"
	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/gitx"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// exitDeclined is `review round`'s "no next round": a normal end for the
// review step, which prints the decline and stops.
const exitDeclined = 3

// runReviewRound needs no remote review declared: the host is read only for
// the PR a series belongs to, and a branch checked out elsewhere, or a
// subject that does not resolve, gets the rounds the directory holds.
func runReviewRound(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	name, err := reviewsKey(ctx, t, cmd.Args().First())
	if err != nil {
		return err
	}

	dir := filepath.Join(t.lay.StorageRoot, "reviews", name)

	var series roundSeries

	if s, err := resolvePRSubject(ctx, t, cmd.Args().First()); err == nil && s.here {
		if series, err = seriesOf(ctx, t, s, dir, cmd.ErrWriter); err != nil {
			return err
		}
	}

	st := findings.Status{Dir: dir, Decline: findings.Merged, Latest: latestRound(dir)}
	if !series.merged {
		if st, err = findings.Rounds(dir, numbered(dir)); err != nil {
			return err
		}
	}

	st.PR, st.Archived = series.pr, series.archived

	if cmd.Bool("json") {
		enc := json.NewEncoder(cmd.Writer)
		enc.SetIndent("", "  ")

		if err := enc.Encode(st); err != nil {
			return fmt.Errorf("encoding rounds: %w", err)
		}
	} else if _, err := fmt.Fprint(cmd.Writer, roundsDoc(st)); err != nil {
		return err
	}

	if st.Decline != "" {
		return cli.Exit("", exitDeclined)
	}

	return nil
}

// reviewsKey names a task's findings directory: the task id (a bundle's
// lead), else the branch with `/` as `-` for work no task names.
func reviewsKey(ctx context.Context, t *target, explicit string) (string, error) {
	r, err := t.reference(ctx, explicit)
	if err == nil {
		return r.Head().ID, nil
	}

	if !errors.Is(err, ref.ErrNoReference) {
		return "", err
	}

	branch := gitx.CurrentBranch(ctx, t.dir)
	if branch == "" {
		return "", fmt.Errorf("%w, and no branch is checked out to name the findings directory", err)
	}

	return strings.ReplaceAll(branch, "/", "-"), nil
}

// A roundSeries is what the branch's state says about its findings
// directory before the rounds are counted.
type roundSeries struct {
	merged   bool   // main already holds the branch's content
	pr       string // the open PR for the next header, `none` without one
	archived string // where a finished series moved
}

// seriesOf checks for a merged branch first, so a branch left over after its
// PR merged declines instead of archiving rounds and opening a new series.
func seriesOf(ctx context.Context, t *target, s prSubject, dir string, warn io.Writer) (roundSeries, error) {
	series := roundSeries{pr: "none"}

	h, hostErr := t.host()
	if s.number > 0 {
		series.pr = "#" + strconv.Itoa(s.number)
		if hostErr == nil && h.GLab() {
			series.pr = "!" + strconv.Itoa(s.number)
		}
	}

	base, err := s.base(ctx, io.Discard)
	if err == nil {
		series.merged = alreadyMerged(ctx, s.path, base)
	}

	if series.merged {
		return series, nil
	}

	latest := latestRound(dir)
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

	if !seriesClosed(ctx, s.path, f, h, hostErr, warn) {
		return series, nil
	}

	head, _, _ := strings.Cut(f.Reviewed, " ")
	restart := !digits(strings.TrimLeft(f.PR, "#!"))
	series.archived, err = archiveReviews(dir, seriesEnd{pr: cmp.Or(f.PR, "none"), head: head, restart: restart})

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

	head, err := capturedHead(ctx, dir)
	if err != nil {
		return false
	}

	into, tree, err := mergedTree(ctx, dir, head, base)
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
func seriesClosed(ctx context.Context, dir string, f *findings.File, h host, hostErr error, warn io.Writer) bool {
	if n := strings.TrimLeft(f.PR, "#!"); digits(n) {
		if hostErr != nil {
			fmt.Fprintf(warn, "f10: cannot read %s (%v), so its series of rounds goes on\n", f.PR, hostErr)

			return false
		}

		p, err := h.pullNumber(ctx, n)
		if err != nil {
			fmt.Fprintf(warn, "f10: cannot read %s (%v), so its series of rounds goes on\n", f.PR, err)

			return false
		}

		return p.status() == forge.Merged || p.status() == forge.Closed
	}

	sha, _, _ := strings.Cut(f.Reviewed, " ")
	if sha == "" {
		return false
	}

	reachable, err := gitx.IsAncestor(ctx, dir, sha, "HEAD")

	return err == nil && !reachable
}

// latestRound is the highest-numbered round file in dir, or "".
func latestRound(dir string) string {
	if files := numbered(dir); len(files) > 0 {
		return files[len(files)-1]
	}

	return ""
}

func roundsDoc(st findings.Status) string {
	var b strings.Builder

	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%-8s %s\n", k+":", v)
		}
	}

	row("dir", st.Dir)
	row("archive", st.Archived)
	row("latest", st.Latest)
	row("pr", st.PR)

	switch st.Decline {
	case findings.Merged:
		row("decline", "already merged, nothing to review")
	case findings.Unsettled:
		row("decline", "round 1 has findings without a verdict: run /f10:resolve")
	case findings.Exhausted:
		why := "every finding is settled"
		if len(st.Open) > 0 {
			why = "these wait: run /f10:resolve"
		}

		row("decline", "round "+strconv.Itoa(findings.LastRound)+" is the last; "+why)
	default:
		row("round", strconv.Itoa(st.Round))
		row("path", st.Path)
	}

	for _, o := range st.Open {
		fmt.Fprintf(&b, "  %s %s - %s\n", o.ID, o.Title, o.Why)
	}

	return b.String()
}
