package task_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
)

func childRows(spec ...[2]string) []stack.ChildRow {
	rows := make([]stack.ChildRow, 0, len(spec))

	for _, s := range spec {
		r, _ := ref.Lookup{Prefix: "FT"}.Find(context.Background(), s[0])

		row := stack.ChildRow{ID: r.ID, Title: "t" + r.Number, After: s[1]}
		row.Number = len(rows) + 1
		rows = append(rows, row)
	}

	return rows
}

// The tree follows each After: chain among siblings, one level per step; a
// base outside the siblings and a cycle both land at the top level.
func TestChildTree(t *testing.T) {
	got := stack.ChildTree(childRows(
		[2]string{"FT-72", ""},
		[2]string{"FT-73", "FT-72"},
		[2]string{"FT-74", "FT-72"},
		[2]string{"FT-75", "FT-74"},
		[2]string{"FT-76", "FT-75"},
		[2]string{"FT-77", "FT-9"},
		[2]string{"FT-78", "FT-79"},
		[2]string{"FT-79", "FT-78"},
	))

	var order []string
	for _, r := range got {
		order = append(order, r.ID+":"+string(rune('0'+r.Depth)))
	}

	want := "FT-72:0 FT-73:1 FT-74:1 FT-75:2 FT-76:3 FT-77:0 FT-78:0 FT-79:1"
	if s := strings.Join(order, " "); s != want {
		t.Errorf("tree = %s\nwant   %s", s, want)
	}

	var out bytes.Buffer
	if err := taskcmd.WriteTree(&out, got[:5]); err != nil {
		t.Fatal(err)
	}

	wantTree := `FT-72  t72
├─ FT-73  t73  after FT-72
└─ FT-74  t74  after FT-72
   └─ FT-75  t75  after FT-74
      └─ FT-76  t76  after FT-75
`
	if out.String() != wantTree {
		t.Errorf("writeTree =\n%s\nwant\n%s", out.String(), wantTree)
	}
}

// --open drops the finished rows and re-trees the rest: a dependent of a
// finished base moves up to its base's level instead of hanging under the
// row before it. Rows with urls print none.
func TestOpenPlainTree(t *testing.T) {
	rows := stack.ChildTree(childRows(
		[2]string{"FT-72", ""},
		[2]string{"FT-73", ""},
		[2]string{"FT-74", "FT-73"},
		[2]string{"FT-75", "FT-74"},
	))
	for i := range rows {
		rows[i].URL = "https://tracker.example/" + strings.TrimPrefix(rows[i].ID, "FT-")
		rows[i].Finished = rows[i].ID == "FT-73"
	}

	var out bytes.Buffer
	if err := taskcmd.WriteTree(&out, taskcmd.OpenRows(rows)); err != nil {
		t.Fatal(err)
	}

	// urls stay in --json: the tree a model reads carries none
	want := `FT-72  t72
FT-74  t74  after FT-73
└─ FT-75  t75  after FT-74
`
	if out.String() != want {
		t.Errorf("open plain tree =\n%s\nwant\n%s", out.String(), want)
	}
}
