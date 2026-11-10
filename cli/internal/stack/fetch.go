package stack

import (
	"context"

	"github.com/amberpixels/f10/cli/internal/gitx"
)

// A FetchOnce fetches origin at most once, a tick's or a watcher pass's:
// every worktree shares the main checkout's remote refs, so one fetch serves
// them all. A failed fetch is not kept, so the next caller tries again.
type FetchOnce struct{ Done bool }

func (f *FetchOnce) Fetch(ctx context.Context, dir string) error {
	if f.Done {
		return nil
	}

	if err := gitx.Fetch(ctx, dir); err != nil {
		return err
	}

	f.Done = true

	return nil
}
