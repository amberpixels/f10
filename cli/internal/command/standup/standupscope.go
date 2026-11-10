package standup

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/scope"
	"github.com/amberpixels/f10/cli/internal/standup"
	"github.com/amberpixels/f10/cli/internal/target"
)

// standupTracker keys where the target's task ids live. A host's issues
// belong to the repository (GH-12 in two repositories is two issues), so
// each is its own project; any other tracker is shared by kind and prefix.
func standupTrackerFor(t *target.Target) (standup.Tracker, string) {
	tr := standup.Tracker{Kind: t.Effective.TrackerKind.String(), Prefix: t.Effective.IDPrefix}

	if tr.Prefix != "" && !t.HostCloses() {
		tr.Key = "tracker\x00" + tr.Kind + "\x00" + tr.Prefix

		return tr, tr.Prefix
	}

	tr.Key = "repo\x00" + cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot)

	return tr, t.Layout.Project
}

// collectTarget collects one checkout, its repository and tracker named.
func collectTarget(ctx context.Context, t *target.Target, since time.Time) (standup.Input, error) {
	tr, project := standupTrackerFor(t)
	src := standup.Source{
		Dir:    t.Layout.CheckoutRoot,
		Main:   cmp.Or(t.Layout.MainRoot, t.Layout.CheckoutRoot),
		Prefix: t.Effective.IDPrefix,
		Upkeep: t.Effective.UpkeepPaths,
	}

	if h := hostops.OptionalHostOf(t); h != nil {
		src.Host = &h.Host
	}

	in, err := standup.Collect(ctx, src, since)
	if err != nil {
		return standup.Input{}, err
	}

	in.Repo, in.Project, in.Tracker = t.Layout.Project, project, tr

	return in, nil
}

// collectAll collects every target, a few at a time, keeping the scope's
// order. A checkout that fails fails the run, named: dropping it would
// leave its work out of a report meant to be whole.
func collectAll(ctx context.Context, targets []*target.Target, since time.Time) ([]standup.Input, error) {
	if len(targets) == 1 {
		in, err := collectTarget(ctx, targets[0], since)

		return []standup.Input{in}, err
	}

	ins := make([]standup.Input, len(targets))
	errs := make([]error, len(targets))
	sem := make(chan struct{}, scope.ScopeParallel)

	var wg sync.WaitGroup

	for i, t := range targets {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			ins[i], errs[i] = collectTarget(ctx, t, since)
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("%s: %w", targets[i].Layout.CheckoutRoot, err)
		}
	}

	return ins, nil
}
