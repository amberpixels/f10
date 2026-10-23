package main

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/amberpixels/f10/cli/internal/standup"
)

// standupTracker is where the target's task ids live and the project its
// report goes under. A host's issues belong to the repository: GH-12 in two
// repositories is two issues, so each is its own project. Any other tracker
// is shared by declaration, keyed by its kind and prefix, so repositories
// on one Jira or Linear project merge their tasks.
func (t *target) standupTracker() (standup.Tracker, string) {
	tr := standup.Tracker{Kind: t.eff.TrackerKind, Prefix: t.eff.IDPrefix}

	if tr.Prefix != "" && (t.driver() != nil || !t.hostIssues()) {
		tr.Key = "tracker\x00" + tr.Kind + "\x00" + tr.Prefix

		return tr, tr.Prefix
	}

	tr.Key = "repo\x00" + cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot)

	return tr, t.lay.Project
}

// collectTarget collects one checkout, its repository and tracker named.
func collectTarget(ctx context.Context, t *target, since time.Time) (standup.Input, error) {
	h, hostErr := t.host()
	tr, project := t.standupTracker()

	in, err := collectStandup(ctx, standupSource{
		dir: t.lay.CheckoutRoot, main: cmp.Or(t.lay.MainRoot, t.lay.CheckoutRoot), prefix: t.eff.IDPrefix,
		upkeep: t.eff.UpkeepPaths, host: h, noHost: hostErr != nil,
	}, since)
	if err != nil {
		return standup.Input{}, err
	}

	in.Repo, in.Project, in.Tracker = t.lay.Project, project, tr

	return in, nil
}

// collectAll collects every target, a few at a time, keeping the scope's
// order. A checkout that fails fails the run, named: dropping it would
// leave its work out of a report meant to be whole.
func collectAll(ctx context.Context, targets []*target, since time.Time) ([]standup.Input, error) {
	if len(targets) == 1 {
		in, err := collectTarget(ctx, targets[0], since)

		return []standup.Input{in}, err
	}

	ins := make([]standup.Input, len(targets))
	errs := make([]error, len(targets))
	sem := make(chan struct{}, scopeParallel)

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
			return nil, fmt.Errorf("%s: %w", targets[i].lay.CheckoutRoot, err)
		}
	}

	return ins, nil
}
