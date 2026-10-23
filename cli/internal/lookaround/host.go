package lookaround

import (
	"context"
	"fmt"

	"github.com/amberpixels/f10/cli/internal/forge"
	"github.com/amberpixels/f10/cli/internal/trace"
)

// collectHost reads every open request and every one merged in the
// window, the state block in each body, the trailers on each one's
// commits, whether a merged one's branch is still there, and whether the
// declared remote reviewer has one waiting. The list failing fails the
// source; a read on one request is a note, the request kept without it.
func collectHost(ctx context.Context, in Inputs) ([]PR, []string, error) {
	pulls, err := in.Host.PullsSince(ctx, in.Since)
	if err != nil {
		return nil, nil, err
	}

	var notes []string

	out := make([]PR, 0, len(pulls))

	for _, p := range pulls {
		pr := PR{PR: p}

		if s, ok := trace.ParseState(p.Body); ok {
			pr.Block = &s
		}

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

		switch {
		case p.Status == forge.Merged && p.Head != "":
			exists, err := in.Host.BranchExists(ctx, p.Head)
			if err != nil {
				notes = append(notes, fmt.Sprintf("#%d: branch %s not checked: %v", p.Number, p.Head, err))
			} else {
				pr.HeadExists = &exists
			}
		case p.Status == forge.Open && in.RemoteOpen != nil:
			pr.RemoteOpen = in.RemoteOpen(ctx, p.Number)
		}

		out = append(out, pr)
	}

	return out, notes, nil
}
