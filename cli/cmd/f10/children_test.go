package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	taskcmd "github.com/amberpixels/f10/cli/internal/command/task"
	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/hostops"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/seam"
	"github.com/amberpixels/f10/cli/internal/shell"
	"github.com/amberpixels/f10/cli/internal/stack"
	"github.com/amberpixels/f10/cli/internal/storage"
	"github.com/amberpixels/f10/cli/internal/target"
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

func childTarget(t *testing.T, cli string) *target.Target {
	t.Helper()

	root := t.TempDir()

	return &target.Target{
		Dir:       root,
		Layout:    &layout.Layout{CheckoutRoot: root, StorageRoot: storage.InRepo(root)},
		Probes:    &probe.Probes{CLI: probe.Signal{Value: cli}},
		Effective: &facts.Effective{IDPrefix: "FT"},
	}
}

func scriptNoBranch(f *fakes, ids ...string) {
	for _, id := range ids {
		f.script(refsKey("refs/heads/", id), "")
		f.script(refsKey("refs/remotes/origin/", id), "")
	}
}

// GitHub sub-issues come back open only, in the project's id format, with
// the After: base resolved, the labels carried and the archive read.
func TestChildrenFromGitHub(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")

	f.script("gh api --paginate repos/{owner}/{repo}/issues/71/sub_issues", `[
		{"number":72,"title":"base","state":"open","html_url":"https://x/72","body":"b","labels":[{"name":"enhancement"}]},
		{"number":73,"title":"later","state":"open","html_url":"https://x/73","body":"x\nAfter: #72\n","labels":[{"name":"draft"}]},
		{"number":74,"title":"done","state":"closed","html_url":"https://x/74","body":"","labels":[]},
		{"number":75,"title":"archived","state":"open","html_url":"https://x/75","body":"","labels":[]}
	]`)
	scriptNoBranch(f, "FT-72", "FT-73", "FT-75")

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

// A host without sub-issues answers 404, which is no parent link.
func TestChildrenGitHub404(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "gh")

	f.fail("gh api --paginate repos/{owner}/{repo}/issues/71/sub_issues",
		shell.Result{Code: 1, Stderr: "gh: Not Found (HTTP 404)"})

	_, err := stack.TrackerFor(tg).Children(t.Context(), ref.Ref{ID: "FT-71", Number: "71"})
	if !errors.Is(err, hostops.ErrNoParentLink) {
		t.Errorf("err = %v, want errNoParentLink", err)
	}
}

// A driver declining the verb on a tracker no host reaches has no parent
// link; on a host tracker the host answers.
func TestChildrenDriverDeclines(t *testing.T) {
	newFakes(t)

	tg := childTarget(t, "gh")
	writeDriver(t, tg.Layout.StorageRoot.Path())
	tg.Effective = &facts.Effective{
		IDPrefix:    "FT",
		TrackerKind: facts.TrackerNotion,
		Facts:       []facts.Fact{{Key: "tracker.kind", Value: "Notion", Origin: facts.DeclaredFrontmatter("main")}},
	}

	seam.Swap(t, &shell.Capture, func(context.Context, string, string, ...string) (shell.Result, error) {
		return shell.Result{Code: 3}, nil
	})

	if _, err := stack.RawChildrenFor(
		t.Context(),
		tg,
		ref.Ref{ID: "FT-71", Number: "71"},
	); !errors.Is(
		err,
		hostops.ErrNoParentLink,
	) {
		t.Errorf("err = %v, want errNoParentLink", err)
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
		"no parent link": {children: func(context.Context, ref.Ref) ([]stack.ChildRow, error) { return nil, hostops.ErrNoParentLink }},
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
			got, err := taskcmd.OpenSubtasks(t.Context(), stack.Tracker{Children: tc.children}, r)

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
	raw := []hostops.RawChild{
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
