package statusview

import (
	"context"
	"io"
	"maps"
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

	for id := range FinishedIn(ctx, t, ids) {
		if k, ok := known[id]; ok {
			k.Closed = true
			known[id] = k
		}
	}

	return known
}

// FinishedIn is which of ids the ledger records a standing finish for: a
// local read, so a closed task is marked with no tracker call. A ledger that
// cannot be read finishes nothing.
func FinishedIn(ctx context.Context, t *target.Target, ids []string) map[string]bool {
	project := t.RefProject(ctx)
	if project == "" || len(ids) == 0 {
		return nil
	}

	events, err := ledger.ReadEvents(ctx, ledger.Path(), ledger.Filter{Project: project, Kind: ledger.TaskFinished})
	if err != nil || len(events) == 0 {
		return nil
	}

	want := map[string]string{}
	for _, id := range ids {
		want[strings.ToUpper(id)] = id
	}

	resolve := t.IDOf(ctx)
	out := map[string]bool{}

	for _, e := range events {
		id, ok := want[strings.ToUpper(resolve(e.FinishedTask()))]
		if ok && lookaround.LedgerFinishStands(true, t.Layout.StorageRoot.PlanLive(id)) {
			out[id] = true
		}
	}

	return out
}

//nolint:lostfield // a lookup through the target, not a conversion of it
func lookupRefFor(ctx context.Context, t *target.Target, r ref.Ref) (ledger.TaskRef, error) {
	return tasksource.TasksFor(t).Ref(ctx, r)
}
