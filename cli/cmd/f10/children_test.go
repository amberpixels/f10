package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amberpixels/f10/cli/internal/facts"
	"github.com/amberpixels/f10/cli/internal/layout"
	"github.com/amberpixels/f10/cli/internal/probe"
	"github.com/amberpixels/f10/cli/internal/ref"
	"github.com/amberpixels/f10/cli/internal/shell"
)

func childRows(spec ...[2]string) []childRow {
	rows := make([]childRow, 0, len(spec))

	for _, s := range spec {
		r, _ := ref.Lookup{Prefix: "FT"}.Find(context.Background(), s[0])

		row := childRow{ID: r.ID, Title: "t" + r.Number, After: s[1]}
		row.number = len(rows) + 1
		rows = append(rows, row)
	}

	return rows
}

// The tree follows each After: chain among siblings, one level per step; a
// base outside the siblings and a cycle both land at the top level.
func TestChildTree(t *testing.T) {
	got := childTree(childRows(
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
	if err := writeTree(&out, got[:5]); err != nil {
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

// childTarget is a GitHub or GitLab target with no driver and an empty
// plan archive, under the fakes.
func childTarget(t *testing.T, cli string) *target {
	t.Helper()

	root := t.TempDir()

	return &target{
		dir: root,
		lay: &layout.Layout{CheckoutRoot: root, StorageRoot: filepath.Join(root, ".f10")},
		pb:  &probe.Probes{CLI: probe.Signal{Value: cli}},
		eff: &facts.Effective{IDPrefix: "FT"},
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

	tr := tg.tracker()

	if err := os.MkdirAll(tr.archive[0], 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(tr.archive[0], "FT-75.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := tr.children(t.Context(), ref.Ref{ID: "FT-71", Number: "71"})
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

	_, err := tg.tracker().children(t.Context(), ref.Ref{ID: "FT-71", Number: "71"})
	if !errors.Is(err, errNoParentLink) {
		t.Errorf("err = %v, want errNoParentLink", err)
	}
}

// GitLab children come from the work-item hierarchy.
func TestChildrenFromGitLab(t *testing.T) {
	f := newFakes(t)
	tg := childTarget(t, "glab")

	f.script("glab repo view -F json", `{"path_with_namespace":"group/repo"}`)
	f.script("glab api graphql -f query="+childItemsQuery+" -f path=group/repo -f iid=71", `{"data":{"project":{
		"workItems":{"nodes":[{"widgets":[{},{"children":{"nodes":[
			{"iid":"72","title":"base","state":"OPEN","webUrl":"https://g/72","description":"",
			 "widgets":[{},{"labels":{"nodes":[{"title":"draft"}]}}]},
			{"iid":"73","title":"gone","state":"CLOSED","webUrl":"https://g/73","description":"","widgets":[]}
		]}}]}]}}}}`)
	scriptNoBranch(f, "FT-72")

	rows, err := tg.tracker().children(t.Context(), ref.Ref{ID: "FT-71", Number: "71"})
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 1 || rows[0].ID != "FT-72" || rows[0].Labels[0] != "draft" || rows[0].URL != "https://g/72" {
		t.Errorf("rows = %+v", rows)
	}
}

// A driver declining the verb on a tracker no host reaches has no parent
// link; on a host tracker the host answers.
func TestChildrenDriverDeclines(t *testing.T) {
	newFakes(t)

	tg := childTarget(t, "gh")
	writeDriver(t, tg.lay.StorageRoot)
	tg.eff = &facts.Effective{
		IDPrefix:    "FT",
		TrackerKind: "Notion",
		Facts:       []facts.Fact{{Key: "tracker.kind", Value: "Notion", Origin: "declared (main, frontmatter)"}},
	}

	shell.Capture = func(context.Context, string, string, ...string) (shell.Result, error) {
		return shell.Result{Code: 3}, nil
	}

	if _, err := tg.rawChildren(t.Context(), ref.Ref{ID: "FT-71", Number: "71"}); !errors.Is(err, errNoParentLink) {
		t.Errorf("err = %v, want errNoParentLink", err)
	}
}

func TestOpenSubtasks(t *testing.T) {
	r := ref.Ref{ID: "FT-1", Number: "1"}
	failed := errors.New("boom")

	for name, tc := range map[string]struct {
		children func(context.Context, ref.Ref) ([]childRow, error)
		want     []string
		err      string
	}{
		"no lookup":      {},
		"no parent link": {children: func(context.Context, ref.Ref) ([]childRow, error) { return nil, errNoParentLink }},
		"a read error": {
			children: func(context.Context, ref.Ref) ([]childRow, error) { return nil, failed },
			err:      "FT-1: reading its subtasks: boom",
		},
		"unfinished rows in order": {
			children: func(context.Context, ref.Ref) ([]childRow, error) {
				return []childRow{{ID: "FT-3"}, {ID: "FT-2", Finished: true}, {ID: "FT-4"}}, nil
			},
			want: []string{"FT-3", "FT-4"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := openSubtasks(t.Context(), tracker{children: tc.children}, r)

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
