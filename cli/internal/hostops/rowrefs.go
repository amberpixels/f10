package hostops

import (
	"github.com/amberpixels/f10/cli/internal/ledger"
	"github.com/amberpixels/f10/cli/internal/tasktitle"
)

func RowRefs(rows []Row) []ledger.TaskRef {
	refs := make([]ledger.TaskRef, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, ledger.TaskRef{ID: r.ID, Title: tasktitle.Clean(r.Title, r.ID), URL: r.URL})
	}

	return refs
}
