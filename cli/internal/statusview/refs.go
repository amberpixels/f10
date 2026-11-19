package statusview

import (
	"context"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/lookaround"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasksource"
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

	// a tracker read made in this call knows better than a finish, which a
	// reopen never clears
	var cached []ref.Ref

	for _, r := range refs {
		if r.ID != "" && !slices.ContainsFunc(read, func(k ledger.TaskRef) bool { return k.ID == r.ID }) {
			cached = append(cached, r)
		}
	}

	for id := range FinishedIn(ctx, t, cached) {
		if k, ok := known[id]; ok {
			k.Closed = true
			known[id] = k
		}
	}

	return known
}

// FinishedIn is which of refs the ledger records a standing finish for: a
// local read, so a closed task is marked with no tracker call. A ledger that
// cannot be read finishes nothing.
func FinishedIn(ctx context.Context, t *target.Target, refs []ref.Ref) map[string]bool {
	project := t.RefProject(ctx)
	if project == "" || len(refs) == 0 {
		return nil
	}

	events, err := ledger.ReadEvents(ctx, ledger.Path(), ledger.Filter{Project: project, Kind: ledger.TaskFinished})
	if err != nil || len(events) == 0 {
		return nil
	}

	var resolve func(string) string

	out := map[string]bool{}

	for _, e := range events {
		token := strings.TrimPrefix(e.FinishedTask(), "#")
		if token == "" {
			continue // an empty token would resolve to this checkout's own task
		}

		for _, r := range refs {
			hit := strings.EqualFold(token, r.ID) || token == r.Number
			if !hit && strings.Contains(token, "://") {
				// only a url needs the target to name its task
				if resolve == nil {
					resolve = t.IDOf(ctx)
				}

				hit = strings.EqualFold(resolve(token), r.ID)
			}

			if hit && lookaround.LedgerFinishStands(true, t.Layout.StorageRoot.PlanLive(r.ID), false, false) {
				out[r.ID] = true
			}
		}
	}

	return out
}

//nolint:lostfield // a lookup through the target, not a conversion of it
func lookupRefFor(ctx context.Context, t *target.Target, r ref.Ref) (ledger.TaskRef, error) {
	return tasksource.TasksFor(t).Ref(ctx, r)
}
