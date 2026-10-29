package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/ref"
)

// The words task stale prints; fetch acts on each and works out nothing.
const (
	staleFresh   = "fresh"
	staleJudged  = "judged"
	staleNeeds   = "needs judge"
	staleUndated = "undated"
)

func runTaskStale(ctx context.Context, cmd *cli.Command) error {
	t, err := targetFor(ctx, cmd)
	if err != nil {
		return err
	}

	r, err := t.reference(ctx, cmd.Args().First())
	if err != nil {
		return err
	}

	// a bundle's verdicts are keyed on its lead, as its plan is
	r = r.Head()

	var created time.Time

	// no stale_after: nothing to judge, so the tracker is never asked
	if _, err := facts.ParseStaleAfter(t.eff.StaleAfter); err == nil {
		if created, err = t.createdAt(ctx, r, cmd.String("created")); err != nil {
			return err
		}
	}

	roots := []string{t.lay.StorageRoot}
	if t.lay.LegacyStorage && t.lay.MainRoot != "" {
		roots = append(roots, filepath.Join(t.lay.MainRoot, ".f10"))
	}

	_, err = fmt.Fprintln(cmd.Writer, staleness(created, t.eff.StaleAfter, time.Now(), roots, r.ID))

	return err
}

// createdAt reads the date from the host's issues only: a driver's doc shows
// it in any form, so the agent that read it passes it as --created.
func (t *target) createdAt(ctx context.Context, r ref.Ref, flag string) (time.Time, error) {
	if flag != "" {
		at, err := time.Parse(time.DateOnly, flag)
		if err != nil {
			return time.Time{}, fmt.Errorf("--created %q: want YYYY-MM-DD", flag)
		}

		return at, nil
	}

	if !t.hostIssues() || t.driver() != nil {
		return time.Time{}, nil
	}

	h, err := t.host()
	if err != nil {
		return time.Time{}, err
	}

	at, err := h.issueCreated(ctx, r.Number)
	if err != nil {
		return time.Time{}, fetchFallback(err)
	}

	return at, nil
}

// staleness is the line task stale prints. A proceed on the task within
// stale_after clears it, searched in every root a worktree's verdicts may sit in.
func staleness(created time.Time, staleAfter string, now time.Time, roots []string, id string) string {
	d, err := facts.ParseStaleAfter(staleAfter)

	switch {
	case err != nil:
		return staleFresh
	case created.IsZero():
		return staleUndated
	case !staleAt(created, staleAfter, now):
		return staleFresh
	}

	var newest verdictFile

	for _, root := range roots {
		for _, v := range judgeFiles(root, id) {
			if v.judged == "task" && v.outcome.Verdict == "proceed" && now.Sub(v.at) <= d && v.at.After(newest.at) {
				newest = v
			}
		}
	}

	if newest.path == "" {
		return staleNeeds
	}

	return staleJudged + " " + newest.path
}
