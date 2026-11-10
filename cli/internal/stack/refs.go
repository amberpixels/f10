package stack

import (
	"context"

	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/target"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

// RememberRefsFor records refs as this project's; it never fails its caller.
func RememberRefsFor(ctx context.Context, t *target.Target, refs ...ledger.TaskRef) {
	if len(refs) == 0 {
		return
	}

	ledger.RememberRefs(ctx, t.RefProject(ctx), refs...)
}

func ChildRefs(rows []ChildRow) []ledger.TaskRef {
	refs := make([]ledger.TaskRef, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, ledger.TaskRef{ID: r.ID, Title: tasktitle.Clean(r.Title, r.ID), URL: r.URL})
	}

	return refs
}
