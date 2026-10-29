package lookaround

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// collectHost reads the open and recently merged requests with what the
// board needs beyond the list. The list failing fails the source; a failed
// read on one request, or the branch listing, is a note.
func collectHost(ctx context.Context, in Inputs) ([]PR, []string, error) {
	pulls, err := in.Host.PullsSince(ctx, in.Since)
	if err != nil {
		return nil, nil, err
	}

	var (
		branches map[string]bool
		notes    []string
	)

	if slices.ContainsFunc(pulls, func(p forge.PR) bool { return p.Status == forge.Merged && p.Head != "" }) {
		if branches, err = in.Host.Branches(ctx); err != nil {
			notes = append(notes, fmt.Sprintf("branches not listed: %v", err))
		}
	}

	out := make([]PR, len(pulls))
	perPull := make([][]string, len(pulls))
	sem := make(chan struct{}, pullParallel)

	var wg sync.WaitGroup

	for i, p := range pulls {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			out[i], perPull[i] = readPull(ctx, in, p, branches)
		})
	}

	wg.Wait()

	for _, n := range perPull {
		notes = append(notes, n...)
	}

	return out, notes, nil
}

// pullParallel bounds the host calls in flight for one project: each is a
// gh or glab process, and serial calls cost a busy repo's board a call's
// latency per merged request.
const pullParallel = 8

// readPull is one request with what the board reads beyond the list.
// branches is every branch the host has, nil when it was not listed.
func readPull(ctx context.Context, in Inputs, p forge.PR, branches map[string]bool) (PR, []string) {
	pr := PR{PR: p}

	var notes []string

	if s, ok := trace.ParseState(p.Body); ok {
		pr.Block = &s
	}

	// the commits add only the trailers' task to the join, so they are
	// read only for a request nothing else names a task for
	if pr.Block == nil && ref.Members(in.Prefix, p.Head) == nil &&
		ref.Match(in.Prefix, p.Head) == "" && ref.Match(in.Prefix, p.Title) == "" {
		commits, err := in.Host.PullCommits(ctx, p.Number)
		if err != nil {
			notes = append(notes, fmt.Sprintf("#%d: commits not read: %v", p.Number, err))
		}

		pr.Commits = commits

		for _, c := range commits {
			if t := trace.Parse(c.Message); !t.Zero() || len(t.Steps) > 0 {
				pr.Trailers = append(pr.Trailers, t)
			}
		}
	}

	switch {
	case p.Status == forge.Merged && p.Head != "" && branches != nil:
		exists := branches[p.Head]
		pr.HeadExists = &exists
	case p.Status == forge.Open && in.RemoteOpen != nil:
		open, err := in.RemoteOpen(ctx, p.Number)
		if err != nil {
			notes = append(notes, fmt.Sprintf("#%d: remote review not read: %v", p.Number, err))
		}

		pr.RemoteOpen = open
	}

	return pr, notes
}

// collectActivity reads the window in one query, then the other requests
// of every task opened, merged or closed in it in one more, since how a
// task's requests relate is told by those. Nothing is read per request.
func collectActivity(ctx context.Context, in Inputs) (*forge.Activity, map[string][]forge.PR, []string, error) {
	a, err := in.Host.Activity(ctx, in.Since)
	if err != nil {
		return nil, nil, nil, err
	}

	var ids []string

	for _, t := range a.Requests {
		if t.CreatedAt.Before(in.Since) && t.MergedAt.Before(in.Since) && t.ClosedAt.Before(in.Since) {
			continue
		}

		for _, id := range membersOf(in.Prefix, feedPR(t)) {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}

	earlier, err := in.Host.EarlierRequests(ctx, ids)
	if err != nil {
		return &a, nil, []string{fmt.Sprintf("earlier requests not read: %v", err)}, nil
	}

	return &a, earlier, nil, nil
}

// feedPR is a touched request in the shape membersOf reads: its state
// block and the trailers on the commits its timeline lists.
func feedPR(t forge.Touched) PR {
	pr := PR{PR: t.PR}

	if s, ok := trace.ParseState(t.Body); ok {
		pr.Block = &s
	}

	for _, e := range t.Events {
		if e.Kind != forge.EventCommit && e.Kind != forge.EventMergeFromBase {
			continue
		}

		if p := trace.Parse(e.Message); !p.Zero() || len(p.Steps) > 0 {
			pr.Trailers = append(pr.Trailers, p)
		}
	}

	return pr
}
