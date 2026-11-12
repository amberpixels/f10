package stack_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/tasksource"
	"github.com/amberpixels/f10/cli/internal/testkit"
)

// GitHub sub-issues come back open only, in the project's id format, with
// the After: base resolved, the labels carried and the archive read.
func TestChildrenFromGitHub(t *testing.T) {
	f := testkit.NewFakes(t)
	tg := testkit.ChildTarget(t, "gh")

	f.Script("gh api --paginate repos/{owner}/{repo}/issues/71/sub_issues", `[
		{"number":72,"title":"base","state":"open","html_url":"https://x/72","body":"b","labels":[{"name":"enhancement"}]},
		{"number":73,"title":"later","state":"open","html_url":"https://x/73","body":"x\nAfter: #72\n","labels":[{"name":"draft"}]},
		{"number":74,"title":"done","state":"closed","html_url":"https://x/74","body":"","labels":[]},
		{"number":75,"title":"archived","state":"open","html_url":"https://x/75","body":"","labels":[]}
	]`)
	testkit.ScriptNoBranch(f, "FT-72", "FT-73", "FT-75")

	tr := stack.TrackerFor(tg)

	if err := os.MkdirAll(tr.Storage.Archive(), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(tr.Storage.Archive(), "FT-75.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := tr.Children(t.Context(), ref.Ref{ID: "FT-71", Number: "71"})
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want the three open ones", rows)
	}

	if r := rows[1]; r.ID != "FT-73" || r.After != "FT-72" || r.Depth != 1 || r.Labels[0] != "draft" ||
		r.URL != "https://x/73" {
		t.Errorf("FT-73 = %+v", r)
	}

	if !rows[2].Finished || rows[0].Finished {
		t.Errorf("finished = %v %v, want only the archived FT-75", rows[0].Finished, rows[2].Finished)
	}
}

func TestOpenSubtasks(t *testing.T) {
	r := ref.Ref{ID: "FT-1", Number: "1"}
	failed := errors.New("boom")

	for name, tc := range map[string]struct {
		children func(context.Context, ref.Ref) ([]stack.ChildRow, error)
		want     []string
		err      string
	}{
		"no lookup":      {},
		"no parent link": {children: func(context.Context, ref.Ref) ([]stack.ChildRow, error) { return nil, tasksource.ErrNoParentLink }},
		"a read error": {
			children: func(context.Context, ref.Ref) ([]stack.ChildRow, error) { return nil, failed },
			err:      "FT-1: reading its subtasks: boom",
		},
		"unfinished rows in order": {
			children: func(context.Context, ref.Ref) ([]stack.ChildRow, error) {
				return []stack.ChildRow{{ID: "FT-3"}, {ID: "FT-2", Finished: true}, {ID: "FT-4"}}, nil
			},
			want: []string{"FT-3", "FT-4"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := stack.OpenSubtasks(t.Context(), stack.Tracker{Children: tc.children}, r)

			switch {
			case tc.err != "" && (err == nil || err.Error() != tc.err):
				t.Errorf("err = %v, want %q", err, tc.err)
			case tc.err == "" && err != nil:
				t.Errorf("err = %v", err)
			case strings.Join(got, ",") != strings.Join(tc.want, ","):
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Drive and `task children` drop a closed child; status keeps it, finished,
// in tree order under its base.
func TestShapeChildrenKeepsClosedForStatus(t *testing.T) {
	tr := stack.Tracker{Resolve: func(ctx context.Context, tok string) (ref.Ref, error) {
		return ref.Lookup{Prefix: "FT"}.Find(ctx, tok)
	}}
	raw := []tasksource.RawChild{
		{ID: "FT-2", Title: "second", State: "closed", After: "FT-1"},
		{ID: "FT-1", Title: "first", State: "CLOSED"},
	}

	dropped, err := stack.ShapeChildren(t.Context(), tr, t.TempDir(), raw, false)
	if err != nil || len(dropped) != 0 {
		t.Fatalf("drive's rows = %+v, %v; want none", dropped, err)
	}

	kept, err := stack.ShapeChildren(t.Context(), tr, t.TempDir(), raw, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(kept) != 2 || kept[0].ID != "FT-1" || kept[1].ID != "FT-2" || kept[1].Depth != 1 ||
		!kept[0].Finished || !kept[1].Finished {
		t.Errorf("status rows = %+v, want FT-1 then FT-2 under it, both finished", kept)
	}
}
