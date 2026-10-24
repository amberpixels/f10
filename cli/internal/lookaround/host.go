package lookaround

import (
	"context"
	"fmt"
	"sync"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// collectHost reads every open request and every one merged in the
// window, the state block in each body, the trailers on the commits of a
// request nothing else names a task for, whether a merged one's branch is
// still there, and whether the declared remote reviewer has one waiting.
// The list failing fails the source; a read on one request is a note, the
// request kept without it.
func collectHost(ctx context.Context, in Inputs) ([]PR, []string, error) {
	pulls, err := in.Host.PullsSince(ctx, in.Since)
	if err != nil {
		return nil, nil, err
	}

	out := make([]PR, len(pulls))
	perPull := make([][]string, len(pulls))
	sem := make(chan struct{}, pullParallel)

	var wg sync.WaitGroup

	for i, p := range pulls {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			out[i], perPull[i] = readPull(ctx, in, p)
		})
	}

	wg.Wait()

	var notes []string
	for _, n := range perPull {
		notes = append(notes, n...)
	}

	return out, notes, nil
}

// pullParallel bounds the host calls in flight for one project: each is a
// gh or glab process, and one at a time made a busy repo's board take a
// call's latency per merged request.
const pullParallel = 8

// readPull is one request with what the board reads beyond the list: its
// state block, the trailers when nothing else names a task, whether a
// merged one's branch is still there, the remote reviewer's open review.
func readPull(ctx context.Context, in Inputs, p forge.PR) (PR, []string) {
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
	case p.Status == forge.Merged && p.Head != "":
		exists, err := in.Host.BranchExists(ctx, p.Head)
		if err != nil {
			notes = append(notes, fmt.Sprintf("#%d: branch %s not checked: %v", p.Number, p.Head, err))
		} else {
			pr.HeadExists = &exists
		}
	case p.Status == forge.Open && in.RemoteOpen != nil:
		open, err := in.RemoteOpen(ctx, p.Number)
		if err != nil {
			notes = append(notes, fmt.Sprintf("#%d: remote review not read: %v", p.Number, err))
		}

		pr.RemoteOpen = open
	}

	return pr, notes
}
