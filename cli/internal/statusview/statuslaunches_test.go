package statusview

import (
	"fmt"
	"strings"
	"testing"
)

// Dropping a row moves what hung under it up to its level.
func TestDropRowsLiftsTheDroppedRowsChildren(t *testing.T) {
	rows := []TaskRow{
		{ID: "A", Depth: 0, Stage: StageDone},
		{ID: "B", Depth: 1},
		{ID: "C", Depth: 2, Stage: StageDone},
		{ID: "D", Depth: 3},
		{ID: "E", Depth: 1},
		{ID: "F", Depth: 0},
	}

	got := dropRows(rows, func(r TaskRow) bool { return r.Stage == StageDone })

	var b strings.Builder
	for _, r := range got {
		fmt.Fprintf(&b, "%s%d ", r.ID, r.Depth)
	}

	if want := "B0 D1 E0 F0 "; b.String() != want {
		t.Errorf("rows = %q, want %q", b.String(), want)
	}
}
