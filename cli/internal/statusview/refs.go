package statusview

import (
	"context"
	"io"
	"maps"

	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/ui"
)

// TaskRefsFor asks the tracker only for a task the ledger lacks (or lacks a
// url for, with md), or for every task with fresh; a tracker that cannot
// answer leaves the task out, with a note on warn.
func TaskRefsFor(t *target.Target,
	ctx context.Context,
	refs []ref.Ref,
	md, fresh bool,
	warn io.Writer,
) map[string]ledger.TaskRef {
	project := t.RefProject(ctx)

	var ids []string

	for _, r := range refs {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}

	known := map[string]ledger.TaskRef{}
	if !fresh && project != "" {
		maps.Copy(known, ledger.LookupRefs(ctx, project, ids))
	}

	var read []ledger.TaskRef

	for _, r := range refs {
		if k, ok := known[r.ID]; r.ID == "" || (ok && k.Title != "" && (!md || k.URL != "")) {
			continue
		}

		k, err := lookupRefFor(ctx, t, r)
		if err != nil {
			ui.Warnf(warn, "%s: %v", r.ID, err)
			delete(known, r.ID)

			continue
		}

		known[r.ID] = k
		read = append(read, k)
	}

	if project != "" {
		ledger.RememberRefs(ctx, project, read...)
	}

	return known
}

//nolint:lostfield // a lookup through the target, not a conversion of it
func lookupRefFor(ctx context.Context, t *target.Target, r ref.Ref) (ledger.TaskRef, error) {
	return hostops.TasksFor(t).Ref(ctx, r)
}
