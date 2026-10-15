package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/projects"
	"github.com/amberpixels/f10/cli/internal/standup"
)

// standupParallel is how many checkouts a standup collects at once; the
// host CLI's calls are most of the time.
const standupParallel = 4

// standupTargets is the checkouts a standup reads: every -C in order, then
// the --group groups in the order given, then with --all every checkout
// under F10_ROOTS; the current checkout with none of them. Checkouts of one
// repository (a main checkout and its worktrees) see the same history, so
// the repository is read once, from the first of them given.
func standupTargets(ctx context.Context, cmd *cli.Command) ([]*target, error) {
	sels, groups := checkoutFlags(cmd), cmd.StringSlice("group")

	if len(sels)+len(groups) == 0 && !cmd.Bool("all") {
		t, err := targetFor(ctx, cmd)
		if err != nil {
			return nil, err
		}

		return []*target{t}, nil
	}

	var dirs []string

	for _, sel := range sels {
		dir, err := checkoutDir(sel)
		if err != nil {
			return nil, err
		}

		dirs = append(dirs, dir)
	}

	if len(groups) > 0 {
		found, err := readGroups(groups)
		if err != nil {
			return nil, err
		}

		dirs = append(dirs, found...)
	}

	if cmd.Bool("all") {
		found, err := projects.All()
		if err != nil {
			return nil, err
		}

		dirs = append(dirs, found...)
	}

	var targets []*target

	seen := map[string]bool{}

	for _, dir := range dirs {
		lay := layout.Locate(ctx, dir)

		repo := cmp.Or(lay.MainRoot, lay.CheckoutRoot)
		if seen[repo] {
			continue
		}

		seen[repo] = true

		t, err := targetAt(ctx, dir, true, cmd.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}

		targets = append(targets, t)
	}

	if len(targets) == 0 {
		return nil, errors.New("the scope names no checkout: the groups and roots given matched none")
	}

	return targets, nil
}

// standupTracker is where the target's task ids live and the project its
// report goes under. A host's issues belong to the repository: GH-12 in two
// repositories is two issues, so each is its own project. Any other tracker
// is shared by declaration, keyed by its kind and prefix, so repositories
// on one Jira or Linear project merge their tasks.
func (t *target) standupTracker() (standup.Tracker, string) {
	tr := standup.Tracker{Kind: t.eff.TrackerKind, Prefix: t.eff.IDPrefix}

	kind := t.eff.TrackerKind
	hostIssues := kind == "" || kind == "GitHub Issues" || kind == "GitLab work items" || !t.trackerDeclared()

	if tr.Prefix != "" && (t.driver() != nil || !hostIssues) {
		tr.Key = "tracker\x00" + kind + "\x00" + tr.Prefix

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
	sem := make(chan struct{}, standupParallel)

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
