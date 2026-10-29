package main

import (
	"context"
	"fmt"
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
	if shared := t.sharedRoot(); shared != t.lay.StorageRoot {
		roots = append(roots, shared)
	}

	_, err = fmt.Fprintln(cmd.Writer, staleness(created, t.eff.StaleAfter, time.Now(), roots, r.ID))

	return err
}

// createdAt prefers --created, the date the agent already read; a bare call
// asks the host's issues, and any other tracker has no date f10 can parse.
func (t *target) createdAt(ctx context.Context, r ref.Ref, flag string) (time.Time, error) {
	if flag != "" {
		at, err := time.Parse(time.DateOnly, flag)
		if err != nil {
			return time.Time{}, fmt.Errorf("--created %q: want YYYY-MM-DD", flag)
		}

		return at, nil
	}

	if !t.hostIssues() {
		return time.Time{}, nil
	}

	h, err := t.host()
	if err != nil {
		return time.Time{}, err
	}

	return h.issueCreated(ctx, r.Number)
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
